package session

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// Transfers: copies between machines that the daemon runs.
//
// A copy started from a client is a job here, in the daemon on the machine the
// client is on, so the client can quit and the copy goes on. Every client (the
// GUI, the TUI, a phone, the CLI) sees the same jobs. This daemon reads and
// writes its own disk directly and reaches every other machine's disk through
// that machine's daemon, over the link, with the file verbs (verb_files.go).
// So each direction, this machine to a host, a host to this machine, and one
// host to another, is the same code with a different end.
//
// The bytes travel on bulk streams (federation/bulk.go), which wait behind
// every pane on the link and keep to a window, so a copy does not make typing
// on that machine slow.
//
// Every file goes through stage, hash, rename: the bytes land in a part file
// beside the destination, the part's sha256 is checked against the source's,
// and only then is it renamed into place. A link that drops leaves the part.
// The job waits for the machine, then compares the last MiB of the part with
// the same range of the source and goes on from the part's end when they
// match. A full hash at the end checks the whole result either way.

// transferMaxRunning bounds the jobs that move bytes at once. The rest wait
// in order.
const transferMaxRunning = 3

// transferKeepDone is how long a finished job stays in the list.
const transferKeepDone = 30 * time.Minute

// transferTailCheck is the range a resume compares before it trusts a part.
const transferTailCheck = 1 << 20

// transferHashTimeout bounds one hash of a whole file on another machine.
const transferHashTimeout = 30 * time.Minute

// Endpoint is one end of a copy: a path on this machine (Host empty) or on a
// configured host.
type Endpoint struct {
	Host string `json:"host,omitempty"`
	Path string `json:"path"`
}

func (e Endpoint) String() string {
	if e.Host == "" {
		return e.Path
	}
	return e.Host + ":" + e.Path
}

// Transfer states.
const (
	transferQueued    = "queued"
	transferRunning   = "running"
	transferVerifying = "verifying"
	transferWaiting   = "waiting"
	transferPaused    = "paused"
	transferDone      = "done"
	transferFailed    = "failed"
	transferCancelled = "cancelled"
)

// transferJob is one copy.
type transferJob struct {
	id       string
	src, dst Endpoint
	move     bool
	conflict string
	// private keeps every file it writes owner only, as a drop folder's are,
	// instead of taking the original's permission bits.
	private bool
	created time.Time

	done atomic.Int64

	mu         sync.Mutex
	state      string
	size       int64
	isDir      bool
	files      int
	filesDone  int
	finished   map[string]bool
	current    string
	final      string
	hash       string
	verified   bool
	errText    string
	errCode    string
	resumedAt  int64
	resumes    int
	restarts   int
	started    time.Time
	ended      time.Time
	samples    []rateSample
	cancel     context.CancelFunc
	stop       string // "pause" or "cancel" while an attempt is being stopped
	wake       chan struct{}
	retryAt    time.Time
	lastWindow uint64
}

type rateSample struct {
	at   time.Time
	done int64
}

// transferManager holds the jobs.
type transferManager struct {
	d       *Daemon
	mu      sync.Mutex
	jobs    map[string]*transferJob
	order   []string
	running chan struct{}
}

func newTransferManager(d *Daemon) *transferManager {
	return &transferManager{d: d, jobs: map[string]*transferJob{}, running: make(chan struct{}, transferMaxRunning)}
}

func newTransferID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// start makes a job and runs it in the background.
func (m *transferManager) start(src, dst Endpoint, move bool, conflict string, private bool) *transferJob {
	j := &transferJob{
		id:       newTransferID(),
		src:      src,
		dst:      dst,
		move:     move,
		conflict: conflict,
		private:  private,
		created:  time.Now(),
		state:    transferQueued,
		finished: map[string]bool{},
		wake:     make(chan struct{}, 1),
	}
	m.mu.Lock()
	m.jobs[j.id] = j
	m.order = append(m.order, j.id)
	m.pruneLocked()
	m.mu.Unlock()
	go m.run(j)
	return j
}

func (m *transferManager) get(id string) *transferJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobs[id]
}

// pruneLocked drops finished jobs older than transferKeepDone.
func (m *transferManager) pruneLocked() {
	keep := m.order[:0]
	for _, id := range m.order {
		j := m.jobs[id]
		j.mu.Lock()
		old := !j.ended.IsZero() && time.Since(j.ended) > transferKeepDone
		j.mu.Unlock()
		if old {
			delete(m.jobs, id)
			continue
		}
		keep = append(keep, id)
	}
	m.order = keep
}

func (j *transferJob) set(f func(j *transferJob)) {
	j.mu.Lock()
	f(j)
	j.mu.Unlock()
}

func (j *transferJob) getState() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state
}

// run drives one job to its end: it takes a running slot, makes attempts, and
// between attempts waits for a machine that went away or for a person who
// paused it.
func (m *transferManager) run(j *transferJob) {
	ctx := m.d.ctx
	backoff := []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 5 * time.Second}
	tries := 0
	for {
		if st := j.getState(); st == transferPaused {
			select {
			case <-j.wake:
				continue
			case <-ctx.Done():
				return
			}
		} else if st == transferCancelled {
			return
		}

		select {
		case m.running <- struct{}{}:
		case <-ctx.Done():
			return
		}
		actx, cancel := context.WithCancel(ctx)
		// A job paused or cancelled while it waited for its slot was told so
		// with no attempt to stop. It must not start one now.
		held := false
		j.set(func(j *transferJob) {
			if j.state == transferPaused || j.state == transferCancelled {
				held = true
				return
			}
			j.cancel = cancel
			if j.started.IsZero() {
				j.started = time.Now()
			}
			if j.state != transferVerifying {
				j.state = transferRunning
			}
			j.errText, j.errCode = "", ""
		})
		if held {
			cancel()
			<-m.running
			continue
		}
		err := m.attempt(actx, j)
		cancel()
		<-m.running

		j.mu.Lock()
		stop := j.stop
		j.stop = ""
		j.cancel = nil
		j.mu.Unlock()

		switch {
		case err == nil:
			j.set(func(j *transferJob) {
				j.state = transferDone
				j.ended = time.Now()
			})
			LogBasic("Transfer %s finished: %s to %s", j.id, j.src, j.dst)
			return
		case stop == "cancel":
			m.abortParts(j)
			j.set(func(j *transferJob) {
				j.state = transferCancelled
				j.ended = time.Now()
			})
			return
		case stop == "pause":
			j.set(func(j *transferJob) { j.state = transferPaused })
			tries = 0
			continue
		}

		var te *transferError
		if errors.As(err, &te) && !te.retry {
			if te.code == ErrVerbHashMismatch && j.restarts == 0 {
				// One copy that did not match is a part that went wrong. It
				// was removed, so the next attempt starts that file again.
				j.set(func(j *transferJob) { j.restarts++ })
				continue
			}
			j.set(func(j *transferJob) {
				j.state = transferFailed
				j.errText, j.errCode = te.msg, te.code
				j.ended = time.Now()
			})
			LogBasic("Transfer %s failed: %s", j.id, te.msg)
			return
		}
		// Anything else is the way to a machine, which comes back.
		wait := backoff[min(tries, len(backoff)-1)]
		tries++
		host := j.src.Host
		if host == "" {
			host = j.dst.Host
		}
		msg := err.Error()
		if host != "" {
			msg = host + " went away. The copy goes on when it is back."
		}
		j.set(func(j *transferJob) {
			j.state = transferWaiting
			j.errText, j.errCode = msg, ErrVerbHostUnreachable
			j.retryAt = time.Now().Add(wait)
		})
		LogBasic("Transfer %s waits %v: %v", j.id, wait, err)
		select {
		case <-time.After(wait):
		case <-j.wake:
		case <-ctx.Done():
			return
		}
	}
}

// transferError is a failure an attempt cannot get past by trying again.
type transferError struct {
	code  string
	msg   string
	retry bool
}

func (e *transferError) Error() string { return e.msg }

func permanent(code, msg string) error { return &transferError{code: code, msg: msg} }

// classify decides whether an error ends the job or waits for the machine.
// A verb answering with an error is the far machine's word about its files
// and stands; a stream or a link that ended is the way there, which comes
// back.
func classify(err error) error {
	if err == nil {
		return nil
	}
	var te *transferError
	if errors.As(err, &te) {
		return err
	}
	var ve *VerbCallError
	if errors.As(err, &ve) {
		switch ve.Code {
		case ErrVerbHostUnreachable, ErrVerbHostRefused, ErrVerbBusy, ErrVerbTooManyConnections:
			return &transferError{code: ve.Code, msg: ve.Message, retry: true}
		}
		return &transferError{code: ve.Code, msg: ve.Message}
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		v := fileError("copy", pe.Path, err)
		return &transferError{code: v.Code, msg: v.Message}
	}
	return &transferError{code: ErrVerbHostUnreachable, msg: err.Error(), retry: true}
}

// attempt runs the job once from where its parts are.
func (m *transferManager) attempt(ctx context.Context, j *transferJob) error {
	src := fileEnd{d: m.d, host: j.src.Host}
	dst := fileEnd{d: m.d, host: j.dst.Host}

	info, err := src.stat(ctx, j.src.Path)
	if err != nil {
		return classify(err)
	}
	if !info.exists {
		return permanent(ErrVerbNoFile, j.src.String()+" does not exist")
	}

	dstPath := j.dst.Path
	j.mu.Lock()
	final := j.final
	j.mu.Unlock()
	if final != "" && info.isDir {
		// A folder copy keeps the folder it chose on its first attempt.
		dstPath = final
	} else if final == "" {
		existing, err := dst.stat(ctx, dstPath)
		if err != nil {
			return classify(err)
		}
		if existing.exists && info.isDir {
			switch j.conflict {
			case "keep-both":
				dstPath, err = dst.freeName(ctx, dstPath)
				if err != nil {
					return classify(err)
				}
			case "replace", "merge":
			default:
				return permanent(ErrVerbFileExists, j.dst.String()+" already exists")
			}
		} else if existing.exists && j.conflict != "replace" && j.conflict != "keep-both" {
			return permanent(ErrVerbFileExists, j.dst.String()+" already exists")
		}
		if info.isDir {
			j.set(func(j *transferJob) { j.final = dstPath })
		}
	}

	if j.private {
		info.perm = 0
	}
	if !info.isDir {
		j.set(func(j *transferJob) {
			j.size, j.files, j.isDir = info.size, 1, false
			j.current = filepath.Base(j.src.Path)
		})
		got, err := m.copyFile(ctx, j, src, dst, j.src.Path, dstPath, info.size, 0, j.conflict, info.perm)
		if err != nil {
			return classify(err)
		}
		j.set(func(j *transferJob) { j.final = got; j.filesDone = 1 })
		if j.move {
			if err := src.remove(ctx, j.src.Path, false); err != nil {
				return classify(err)
			}
		}
		return nil
	}

	entries, total, err := src.walk(ctx, j.src.Path)
	if err != nil {
		return classify(err)
	}
	files := 0
	for _, e := range entries {
		// The names come from the machine the folder is on, which may not
		// be this one. A name that climbs out of the folder, or starts at
		// the root, would let that machine choose where its bytes land.
		if !safeRel(e.Rel) {
			return permanent(ErrVerbInvalidParams, j.src.String()+" names a file outside the folder: "+echoName(e.Rel)+". Nothing outside the folder was written.")
		}
		if !e.Dir {
			files++
		}
	}
	j.set(func(j *transferJob) { j.size, j.files, j.isDir = total, files, true })
	if err := dst.mkdir(ctx, dstPath); err != nil {
		return classify(err)
	}
	// What the finished files hold counts as done from the start, so a resume
	// shows the copy where it is and not from zero.
	var base int64
	for _, e := range entries {
		if e.Dir {
			if err := dst.mkdir(ctx, joinRemote(dstPath, e.Rel)); err != nil {
				return classify(err)
			}
			continue
		}
		j.mu.Lock()
		doneAlready := j.finished[e.Rel]
		j.mu.Unlock()
		if doneAlready {
			base += e.Size
			continue
		}
		j.set(func(j *transferJob) { j.current = e.Rel })
		perm := e.Perm
		if j.private {
			perm = 0
		}
		if _, err := m.copyFile(ctx, j, src, dst, joinRemote(j.src.Path, e.Rel), joinRemote(dstPath, e.Rel), e.Size, base, "replace", perm); err != nil {
			return classify(err)
		}
		base += e.Size
		j.set(func(j *transferJob) {
			j.finished[e.Rel] = true
			j.filesDone = len(j.finished)
		})
	}
	j.done.Store(total)
	if j.move {
		left, err := removeMoved(ctx, src, j.src.Path, entries)
		if err != nil {
			return classify(err)
		}
		if left {
			j.set(func(j *transferJob) {
				j.errText = "The copy is done. Some items in " + j.src.String() + " were not copied, such as links, pipes or new files, so they stay there."
			})
		}
	}
	return nil
}

// removeMoved removes from a moved folder what the copy carried: each file it
// copied, then each folder that is empty after that, deepest first. A move
// must not remove what it did not copy: a link, a pipe, or a file made in the
// folder while the copy ran stays, and so does the folder that holds it. It
// reports whether anything stayed.
func removeMoved(ctx context.Context, src fileEnd, root string, entries []WalkEntry) (bool, error) {
	gone := func(err error) bool {
		if errors.Is(err, fs.ErrNotExist) {
			return true
		}
		var ve *VerbCallError
		return errors.As(err, &ve) && ve.Code == ErrVerbNoFile
	}
	for _, e := range entries {
		if e.Dir {
			continue
		}
		if err := src.remove(ctx, joinRemote(root, e.Rel), false); err != nil && !gone(err) {
			return false, err
		}
	}
	left := false
	for i := len(entries) - 1; i >= 0; i-- {
		if !entries[i].Dir {
			continue
		}
		if err := src.remove(ctx, joinRemote(root, entries[i].Rel), false); err != nil && !gone(err) {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			left = true
		}
	}
	if err := src.remove(ctx, root, false); err != nil && !gone(err) {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		left = true
	}
	return left, nil
}

// safeRel reports whether rel names a path inside a folder: relative, slash
// separated, and with no empty, "." or ".." part and no NUL.
func safeRel(rel string) bool {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.ContainsRune(rel, 0) {
		return false
	}
	for part := range strings.SplitSeq(rel, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// joinRemote joins a path under a folder on any machine. Paths on every
// machine tuios links to are slash separated.
func joinRemote(dir, rel string) string {
	return strings.TrimSuffix(dir, "/") + "/" + filepath.ToSlash(rel)
}

// copyFile copies one file through its part, resuming a part that is there,
// and returns where the file ended up. base is what the job had done before
// this file, for the progress.
func (m *transferManager) copyFile(ctx context.Context, j *transferJob, src, dst fileEnd, from, to string, size, base int64, conflict string, perm uint32) (string, error) {
	// The source's hash runs beside the copy: on another machine it is a
	// read of the file there, which costs this link nothing.
	type hashed struct {
		sum string
		err error
	}
	srcHash := make(chan hashed, 1)
	go func() {
		hctx, cancel := context.WithTimeout(ctx, transferHashTimeout)
		defer cancel()
		sum, err := src.hash(hctx, from, false, 0, -1)
		srcHash <- hashed{sum, err}
	}()

	part, err := dst.partSize(ctx, to)
	if err != nil {
		return "", err
	}
	offset := int64(0)
	if part > 0 && part <= size {
		n := min(part, int64(transferTailCheck))
		a, aerr := src.hash(ctx, from, false, part-n, n)
		b, berr := dst.hash(ctx, to, true, part-n, n)
		if aerr != nil {
			return "", aerr
		}
		if berr == nil && a == b {
			offset = part
		}
	}
	if offset > 0 {
		j.set(func(j *transferJob) {
			j.resumedAt = base + offset
			j.resumes++
		})
		LogBasic("Transfer %s resumes %s at byte %d of %d", j.id, from, offset, size)
	}
	j.done.Store(base + offset)
	j.sample(true)

	if offset < size {
		r, err := src.openRead(ctx, from, offset)
		if err != nil {
			return "", err
		}
		w, finish, err := dst.openWrite(ctx, to, offset, size-offset)
		if err != nil {
			_ = r.Close()
			return "", err
		}
		stop := context.AfterFunc(ctx, func() {
			_ = r.Close()
			_ = w.Close()
		})
		pw := &progressWriter{w: w, j: j}
		n, cerr := io.CopyBuffer(pw, io.LimitReader(r, size-offset), make([]byte, 256<<10))
		stop()
		_ = r.Close()
		if cerr == nil && n < size-offset {
			cerr = io.ErrUnexpectedEOF
		}
		if cerr != nil {
			_ = w.Close()
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", cerr
		}
		if err := finish(); err != nil {
			return "", err
		}
	}

	j.set(func(j *transferJob) {
		j.state = transferVerifying
		j.done.Store(base + size)
	})
	var h hashed
	select {
	case h = <-srcHash:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if h.err != nil {
		return "", h.err
	}
	got, err := dst.commit(ctx, to, h.sum, conflict, perm)
	if err != nil {
		return "", err
	}
	j.set(func(j *transferJob) {
		j.hash, j.verified = h.sum, true
		j.state = transferRunning
	})
	return got, nil
}

// progressWriter counts the bytes a copy has written.
type progressWriter struct {
	w io.Writer
	j *transferJob
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.j.done.Add(int64(n))
	p.j.sample(false)
	return n, err
}

// sample records the progress for the rate, at most every 200 ms.
func (j *transferJob) sample(force bool) {
	now := time.Now()
	j.mu.Lock()
	defer j.mu.Unlock()
	if force {
		j.samples = j.samples[:0]
	}
	if n := len(j.samples); n > 0 && now.Sub(j.samples[n-1].at) < 200*time.Millisecond {
		return
	}
	j.samples = append(j.samples, rateSample{at: now, done: j.done.Load()})
	cut := 0
	for cut < len(j.samples)-1 && now.Sub(j.samples[cut].at) > 3*time.Second {
		cut++
	}
	j.samples = j.samples[cut:]
}

// rate is the bytes per second over the last two seconds or so.
func (j *transferJob) rateLocked() float64 {
	n := len(j.samples)
	if n < 2 {
		return 0
	}
	last := j.samples[n-1]
	first := j.samples[0]
	for _, s := range j.samples {
		if last.at.Sub(s.at) <= 2*time.Second {
			first = s
			break
		}
	}
	dt := last.at.Sub(first.at).Seconds()
	if dt <= 0 || time.Since(last.at) > 3*time.Second {
		return 0
	}
	return float64(last.done-first.done) / dt
}

// abortParts removes the part a cancelled copy left, on whichever machine.
func (m *transferManager) abortParts(j *transferJob) {
	ctx, cancel := context.WithTimeout(m.d.ctx, 10*time.Second)
	defer cancel()
	dst := fileEnd{d: m.d, host: j.dst.Host}
	j.mu.Lock()
	isDir, final, current := j.isDir, j.final, j.current
	j.mu.Unlock()
	target := j.dst.Path
	if isDir && final != "" && current != "" {
		target = joinRemote(final, current)
	}
	_ = dst.abort(ctx, target)
}

// TransferRow is one job in transfer-list.
type TransferRow struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Src         Endpoint `json:"src"`
	Dst         Endpoint `json:"dst"`
	Final       string   `json:"final,omitempty"`
	Kind        string   `json:"kind"`
	Move        bool     `json:"move,omitempty"`
	State       string   `json:"state"`
	Size        int64    `json:"size"`
	Done        int64    `json:"done"`
	Rate        float64  `json:"rate"`
	ETAms       int64    `json:"eta_ms,omitempty"`
	Files       int      `json:"files"`
	FilesDone   int      `json:"files_done"`
	Current     string   `json:"current,omitempty"`
	Error       string   `json:"error,omitempty"`
	Code        string   `json:"code,omitempty"`
	ResumedFrom int64    `json:"resumed_from,omitempty"`
	Resumes     int      `json:"resumes,omitempty"`
	SHA256      string   `json:"sha256,omitempty"`
	Verified    bool     `json:"verified,omitempty"`
	Created     int64    `json:"created"`
	Started     int64    `json:"started,omitempty"`
	Ended       int64    `json:"ended,omitempty"`
	RetryInMs   int64    `json:"retry_in_ms,omitempty"`
}

func (j *transferJob) row() TransferRow {
	j.mu.Lock()
	defer j.mu.Unlock()
	r := TransferRow{
		ID: j.id, Name: filepath.Base(j.src.Path), Src: j.src, Dst: j.dst, Final: j.final,
		Kind: "file", Move: j.move, State: j.state, Size: j.size, Done: j.done.Load(),
		Files: j.files, FilesDone: j.filesDone, Current: j.current,
		Error: j.errText, Code: j.errCode, ResumedFrom: j.resumedAt, Resumes: j.resumes,
		SHA256: j.hash, Verified: j.verified, Created: j.created.UnixMilli(),
	}
	if j.isDir {
		r.Kind = "dir"
	}
	if j.state == transferRunning {
		r.Rate = j.rateLocked()
		// Time left after a second of data, as the plan's row asks.
		if r.Rate > 0 && r.Size > r.Done && len(j.samples) > 0 && time.Since(j.samples[0].at) >= time.Second {
			r.ETAms = int64(float64(r.Size-r.Done) / r.Rate * 1000)
		}
	}
	if j.state == transferWaiting && !j.retryAt.IsZero() {
		r.RetryInMs = max(time.Until(j.retryAt).Milliseconds(), 0)
	}
	if !j.started.IsZero() {
		r.Started = j.started.UnixMilli()
	}
	if !j.ended.IsZero() {
		r.Ended = j.ended.UnixMilli()
	}
	return r
}

func (m *transferManager) rows() []TransferRow {
	m.mu.Lock()
	m.pruneLocked()
	jobs := make([]*transferJob, 0, len(m.order))
	for _, id := range m.order {
		jobs = append(jobs, m.jobs[id])
	}
	m.mu.Unlock()
	out := make([]TransferRow, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.row())
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Created > out[b].Created })
	return out
}

// ---- the two ends ---------------------------------------------------------

// fileEnd is a machine's disk: this one's, read directly, or a host's, through
// its daemon over the link.
type fileEnd struct {
	d    *Daemon
	host string
}

type statResult struct {
	exists bool
	isDir  bool
	size   int64
	perm   uint32
}

func (e fileEnd) local() bool { return e.host == "" }

func (e fileEnd) stat(ctx context.Context, path string) (statResult, error) {
	if e.local() {
		p, verr := expandPath(path)
		if verr != nil {
			return statResult{}, &VerbCallError{Code: verr.Code, Message: verr.Message}
		}
		fi, err := os.Stat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return statResult{}, nil
		}
		if err != nil {
			return statResult{}, err
		}
		return statResult{exists: true, isDir: fi.IsDir(), size: fi.Size(), perm: uint32(fi.Mode().Perm())}, nil
	}
	var r struct {
		Exists bool     `json:"exists"`
		Size   int64    `json:"size"`
		Info   FileInfo `json:"info"`
	}
	if err := e.call(ctx, "file-stat", map[string]any{"path": path}, &r); err != nil {
		return statResult{}, err
	}
	return statResult{exists: r.Exists, isDir: r.Info.isDirLike(), size: r.Size, perm: r.Info.Perm}, nil
}

func (e fileEnd) partSize(ctx context.Context, path string) (int64, error) {
	if e.local() {
		fi, err := os.Stat(partPath(path))
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		return fi.Size(), nil
	}
	var r struct {
		Size int64 `json:"size"`
	}
	err := e.call(ctx, "file-stat", map[string]any{"path": path, "part": true}, &r)
	return r.Size, err
}

func (e fileEnd) hash(ctx context.Context, path string, part bool, off, length int64) (string, error) {
	if e.local() {
		target := path
		if part {
			target = partPath(path)
		}
		sum, _, err := hashRange(ctx, target, off, length)
		return sum, err
	}
	params := map[string]any{"path": path, "offset": off, "part": part}
	if length >= 0 {
		params["length"] = length
	}
	var r struct {
		SHA256 string `json:"sha256"`
	}
	err := e.callTimeout(ctx, "file-hash", params, &r, transferHashTimeout)
	return r.SHA256, err
}

func (e fileEnd) openRead(ctx context.Context, path string, off int64) (io.ReadCloser, error) {
	if e.local() {
		f, _, err := openRegular(path)
		if err != nil {
			return nil, err
		}
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			_ = f.Close()
			return nil, err
		}
		return f, nil
	}
	c, err := e.d.dialHostFiles(ctx, e.host, true)
	if err != nil {
		return nil, err
	}
	if _, err := c.call(ctx, "open-file-stream", map[string]any{"path": path, "mode": "read", "offset": off}, 30*time.Second); err != nil {
		_ = c.Close()
		return nil, err
	}
	return &readCloser{Reader: c.br, c: c}, nil
}

type readCloser struct {
	io.Reader
	c io.Closer
}

func (r *readCloser) Close() error { return r.c.Close() }

// openWrite opens the part of a copy to path at off for length bytes. finish
// ends the write and reports whether every byte landed.
func (e fileEnd) openWrite(ctx context.Context, path string, off, length int64) (io.WriteCloser, func() error, error) {
	if e.local() {
		dir := filepath.Dir(path)
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			if err == nil {
				err = &fs.PathError{Op: "write", Path: dir, Err: fs.ErrNotExist}
			}
			return nil, nil, err
		}
		f, err := openPart(partPath(path))
		if err != nil {
			return nil, nil, err
		}
		if err := f.Truncate(off); err != nil {
			_ = f.Close()
			return nil, nil, err
		}
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			_ = f.Close()
			return nil, nil, err
		}
		return f, func() error {
			if err := f.Sync(); err != nil {
				_ = f.Close()
				return err
			}
			return f.Close()
		}, nil
	}
	c, err := e.d.dialHostFiles(ctx, e.host, true)
	if err != nil {
		return nil, nil, err
	}
	if _, err := c.call(ctx, "open-file-stream", map[string]any{"path": path, "mode": "write", "offset": off, "length": length}, 30*time.Second); err != nil {
		_ = c.Close()
		return nil, nil, err
	}
	finish := func() error {
		defer func() { _ = c.Close() }()
		type res struct {
			line []byte
			err  error
		}
		got := make(chan res, 1)
		go func() {
			line, err := readBoundedLine(c.br, 64<<10)
			got <- res{line, err}
		}()
		var r res
		select {
		case r = <-got:
		case <-time.After(2 * time.Minute):
			return errors.New("the far side did not confirm the write")
		case <-ctx.Done():
			return ctx.Err()
		}
		if r.err != nil {
			return r.err
		}
		var out struct {
			PartSize int64  `json:"part_size"`
			Error    string `json:"error"`
		}
		if err := json.Unmarshal(r.line, &out); err != nil {
			return err
		}
		if out.Error != "" {
			return errors.New(out.Error)
		}
		if out.PartSize != off+length {
			return fmt.Errorf("the part on %s holds %d bytes, want %d", e.host, out.PartSize, off+length)
		}
		return nil
	}
	return c, finish, nil
}

func (e fileEnd) commit(ctx context.Context, path, sum, conflict string, perm uint32) (string, error) {
	if e.local() {
		final, _, verr := commitPart(ctx, path, sum, conflict, perm)
		if verr != nil {
			return "", &VerbCallError{Code: verr.Code, Message: verr.Message}
		}
		return final, nil
	}
	var r struct {
		Path string `json:"path"`
	}
	params := map[string]any{"path": path, "sha256": sum, "conflict": conflict}
	if perm != 0 {
		params["perm"] = perm
	}
	err := e.callTimeout(ctx, "file-commit", params, &r, transferHashTimeout)
	return r.Path, err
}

func (e fileEnd) abort(ctx context.Context, path string) error {
	if e.local() {
		err := os.Remove(partPath(path))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return e.call(ctx, "file-abort", map[string]any{"path": path}, nil)
}

func (e fileEnd) mkdir(ctx context.Context, path string) error {
	if e.local() {
		return os.MkdirAll(path, 0o755)
	}
	return e.call(ctx, "file-mkdir", map[string]any{"path": path}, nil)
}

func (e fileEnd) remove(ctx context.Context, path string, recursive bool) error {
	if e.local() {
		if recursive {
			return os.RemoveAll(path)
		}
		return os.Remove(path)
	}
	return e.call(ctx, "file-remove", map[string]any{"path": path, "recursive": recursive}, nil)
}

func (e fileEnd) walk(ctx context.Context, path string) ([]WalkEntry, int64, error) {
	if e.local() {
		entries, total, err := walkTree(path)
		if errors.Is(err, errWalkTooLarge) {
			return nil, 0, permanent(ErrVerbInvalidParams, err.Error())
		}
		return entries, total, err
	}
	var r struct {
		Entries []WalkEntry `json:"entries"`
		Bytes   int64       `json:"bytes"`
	}
	err := e.callTimeout(ctx, "file-walk", map[string]any{"path": path}, &r, 2*time.Minute)
	return r.Entries, r.Bytes, err
}

// freeName is the first free "name 2", "name 3" ... for a path on this end.
func (e fileEnd) freeName(ctx context.Context, path string) (string, error) {
	if e.local() {
		return freeName(path), nil
	}
	dir, base := filepath.Split(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 2; i < 1000; i++ {
		cand := dir + fmt.Sprintf("%s %d%s", stem, i, ext)
		st, err := e.stat(ctx, cand)
		if err != nil {
			return "", err
		}
		if !st.exists {
			return cand, nil
		}
	}
	return "", permanent(ErrVerbFileExists, "no free name for "+path)
}

func (e fileEnd) call(ctx context.Context, verb string, params any, out any) error {
	return e.callTimeout(ctx, verb, params, out, 30*time.Second)
}

func (e fileEnd) callTimeout(ctx context.Context, verb string, params any, out any, timeout time.Duration) error {
	c, err := e.d.dialHostFiles(ctx, e.host, false)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	raw, err := c.call(ctx, verb, params, timeout)
	if err != nil {
		return err
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// hostFiles is one verb connection to a host's daemon over the link.
type hostFiles struct {
	rw io.ReadWriteCloser
	br *bufio.Reader
	id int
}

func (c *hostFiles) Write(p []byte) (int, error) { return c.rw.Write(p) }
func (c *hostFiles) Close() error                { return c.rw.Close() }

// dialHostFiles opens a connection to host's daemon for the file verbs. A bulk
// connection carries file bytes after its first reply.
func (d *Daemon) dialHostFiles(ctx context.Context, host string, bulk bool) (*hostFiles, error) {
	if d.federation == nil {
		return nil, &VerbCallError{Code: ErrVerbUnknownHost, Message: "no hosts are configured"}
	}
	octx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rw, err := d.federation.OpenConnectionAs(octx, host, federation.StreamOpen{Bulk: bulk})
	if err != nil {
		msg, code := federationErrorText(err)
		if code == ErrVerbUnknownHost {
			return nil, &VerbCallError{Code: code, Message: msg}
		}
		return nil, &VerbCallError{Code: ErrVerbHostUnreachable, Message: msg}
	}
	return &hostFiles{rw: rw, br: bufio.NewReaderSize(rw, 256<<10)}, nil
}

// call sends one verb and reads its one reply line.
func (c *hostFiles) call(ctx context.Context, verb string, params any, timeout time.Duration) (json.RawMessage, error) {
	c.id++
	req := map[string]any{"id": c.id, "verb": verb, "params": params}
	line, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	type res struct {
		line []byte
		err  error
	}
	got := make(chan res, 1)
	go func() {
		if _, err := c.rw.Write(append(line, '\n')); err != nil {
			got <- res{err: err}
			return
		}
		l, err := readBoundedLine(c.br, 16<<20)
		got <- res{l, err}
	}()
	var r res
	select {
	case r = <-got:
	case <-time.After(timeout):
		_ = c.rw.Close()
		return nil, fmt.Errorf("%s did not answer in %v", verb, timeout)
	case <-ctx.Done():
		_ = c.rw.Close()
		return nil, ctx.Err()
	}
	if r.err != nil {
		return nil, r.err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(r.line, &resp); err != nil {
		return nil, fmt.Errorf("the answer to %s did not decode: %w", verb, err)
	}
	if resp.Error != nil {
		return nil, &VerbCallError{Code: resp.Error.Code, Message: resp.Error.Message}
	}
	return resp.Result, nil
}

// readBoundedLine reads one line of at most limit bytes.
func readBoundedLine(br *bufio.Reader, limit int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > limit {
			return nil, errors.New("the answer is larger than the limit")
		}
		if err == nil {
			return buf, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return nil, err
	}
}

// ---- the verbs --------------------------------------------------------------

func (d *Daemon) checkEndpoint(name string, e *Endpoint) *verbError {
	if e.Path == "" {
		return invalidParam(name+".path", name+" needs a path")
	}
	if e.Host == federation.LocalHostName {
		e.Host = ""
	}
	if e.Host == "" {
		p, verr := expandPath(e.Path)
		if verr != nil {
			return verr
		}
		e.Path = p
		return nil
	}
	if !strings.HasPrefix(e.Path, "/") && !strings.HasPrefix(e.Path, "~") {
		return invalidParam(name+".path", "give an absolute path, or one that starts with ~")
	}
	return d.checkHostParam(e.Host)
}

func (d *Daemon) verbTransferStart(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Src      Endpoint `json:"src"`
		Dst      Endpoint `json:"dst"`
		Move     bool     `json:"move"`
		Conflict string   `json:"conflict"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := d.checkEndpoint("src", &p.Src); verr != nil {
		return nil, verr
	}
	if verr := d.checkEndpoint("dst", &p.Dst); verr != nil {
		return nil, verr
	}
	switch p.Conflict {
	case "", "fail", "replace", "keep-both", "merge":
	default:
		return nil, invalidParam("conflict", "conflict is replace, keep-both, merge or fail", "replace", "keep-both", "merge", "fail")
	}
	if p.Src == p.Dst {
		return nil, invalidParam("dst", "the copy would land on the file it copies")
	}
	// A destination that is there is the caller's question to put to the
	// person, so it is answered before a job exists.
	if p.Conflict == "" || p.Conflict == "fail" {
		ctx, cancel := context.WithTimeout(d.ctx, 15*time.Second)
		st, err := fileEnd{d: d, host: p.Dst.Host}.stat(ctx, p.Dst.Path)
		cancel()
		if err != nil {
			var ve *VerbCallError
			if errors.As(err, &ve) {
				return nil, newVerbError(ve.Code, ve.Message)
			}
			return nil, newVerbError(ErrVerbHostUnreachable, err.Error())
		}
		if st.exists {
			return nil, hintedVerbError(ErrVerbFileExists, p.Dst.String()+" already exists", &VerbHint{
				Param:    "conflict",
				Accepted: []string{"replace", "keep-both", "merge"},
				Detail:   "Say what to do with the file that is there: replace it, keep both, or merge a folder into it.",
			})
		}
	}
	j := d.transfers.start(p.Src, p.Dst, p.Move, p.Conflict, false)
	return j.row(), nil
}

func (d *Daemon) verbTransferList(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		ID string `json:"id"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	rows := d.transfers.rows()
	if p.ID != "" {
		for _, r := range rows {
			if r.ID == p.ID {
				return map[string]any{"transfers": []TransferRow{r}}, nil
			}
		}
		return nil, newVerbError(ErrVerbNoTransfer, "no transfer "+echoName(p.ID))
	}
	active := 0
	for _, r := range rows {
		switch r.State {
		case transferDone, transferFailed, transferCancelled:
		default:
			active++
		}
	}
	return map[string]any{"transfers": rows, "active": active}, nil
}

// ErrVerbNoTransfer is a transfer id the daemon does not hold.
const ErrVerbNoTransfer = "no_transfer"

func transferControl(op string) verbHandler {
	return func(d *Daemon, _ *connState, params json.RawMessage) (any, *verbError) {
		var p struct {
			ID string `json:"id"`
		}
		if verr := decodeParams(params, &p); verr != nil {
			return nil, verr
		}
		j := d.transfers.get(p.ID)
		if j == nil {
			return nil, newVerbError(ErrVerbNoTransfer, "no transfer "+echoName(p.ID))
		}
		j.mu.Lock()
		state := j.state
		switch op {
		case "cancel", "pause":
			switch state {
			case transferDone, transferFailed, transferCancelled:
				j.mu.Unlock()
				return j.row(), nil
			}
			if j.cancel != nil {
				j.stop = op
				j.cancel()
			} else if op == "cancel" {
				j.state = transferCancelled
				j.ended = time.Now()
				j.mu.Unlock()
				go d.transfers.abortParts(j)
				select {
				case j.wake <- struct{}{}:
				default:
				}
				return j.row(), nil
			} else {
				j.state = transferPaused
			}
		case "resume":
			switch state {
			case transferPaused, transferWaiting:
				if state == transferPaused {
					j.state = transferQueued
				}
				select {
				case j.wake <- struct{}{}:
				default:
				}
			case transferFailed:
				// Retry: the same job, from its parts.
				j.state = transferQueued
				j.ended = time.Time{}
				j.errText, j.errCode = "", ""
				j.restarts = 0
				go d.transfers.run(j)
			}
		}
		j.mu.Unlock()
		return j.row(), nil
	}
}

func transferVerbs() map[string]verbEntry {
	endpoint := func(name, what string) verbParam {
		return verbParam{Name: name, Type: "object", Required: true, Description: what + ": {host, path}. host is a name from [hosts], or empty for this machine. path is absolute or starts with ~."}
	}
	rowReturn := []verbParam{
		{Name: "id", Type: "string", Description: "The transfer's id."},
		{Name: "state", Type: "string", Description: "queued, running, verifying, waiting (for a machine that went away), paused, done, failed or cancelled.", Accepted: []string{transferQueued, transferRunning, transferVerifying, transferWaiting, transferPaused, transferDone, transferFailed, transferCancelled}},
		{Name: "size", Type: "int", Description: "Bytes to copy in all."},
		{Name: "done", Type: "int", Description: "Bytes copied."},
		{Name: "rate", Type: "float", Description: "Bytes per second over the last two seconds."},
		{Name: "eta_ms", Type: "int", Description: "Time left, once a second of data came."},
		{Name: "resumed_from", Type: "int", Description: "Where the copy went on from after a machine came back. Absent when it never had to."},
		{Name: "verified", Type: "bool", Description: "The copy's sha256 matched the original's."},
		{Name: "final", Type: "string", Description: "Where the file is now, which keep-both can change."},
		{Name: "error", Type: "string", Description: "What happened and what to do, for failed and waiting."},
	}
	idParam := verbParam{Name: "id", Type: "string", Required: true, Description: "The transfer's id."}
	return map[string]verbEntry{
		"transfer-start": {
			description: "Copy a file or a folder between this machine and a host, or between two hosts. The daemon runs the copy, so a client may quit. A machine that goes away pauses it, and it goes on from where it stopped when the machine is back. Every file's sha256 is checked before it is put in place. A destination that exists answers file_exists unless conflict says what to do.",
			params: []verbParam{
				endpoint("src", "What to copy"),
				endpoint("dst", "Where it goes, the full path of the copy"),
				{Name: "move", Type: "bool", Description: "Remove the original once the copy is checked."},
				{Name: "conflict", Type: "string", Description: "When dst exists: replace it, keep both (the copy gets a number), merge a folder into the folder there, or fail.", Accepted: []string{"replace", "keep-both", "merge", "fail"}, Default: "fail"},
			},
			returns:  rowReturn,
			examples: []string{`{"id":1,"verb":"transfer-start","params":{"src":{"host":"build","path":"~/out/image.iso"},"dst":{"path":"~/Downloads/image.iso"}}}`},
			handler:  (*Daemon).verbTransferStart,
		},
		"transfer-list": {
			description: "List the transfers, newest first, with byte progress, rate and time left. Finished ones stay for 30 minutes.",
			params:      []verbParam{{Name: "id", Type: "string", Description: "Only this transfer."}},
			returns: []verbParam{
				{Name: "transfers", Type: "[]object", Description: "One row per transfer: id, name, src, dst, kind, move, state, size, done, rate, eta_ms, files, files_done, current, error, code, resumed_from, resumes, sha256, verified, created, started, ended, retry_in_ms."},
				{Name: "active", Type: "int", Description: "Transfers that are not finished."},
			},
			examples: []string{`{"id":1,"verb":"transfer-list"}`},
			handler:  (*Daemon).verbTransferList,
		},
		"transfer-cancel": {
			description: "Stop a transfer and remove what it copied so far of the file in flight.",
			params:      []verbParam{idParam},
			returns:     rowReturn,
			examples:    []string{`{"id":1,"verb":"transfer-cancel","params":{"id":"3f9a1c2b7d00"}}`},
			handler:     transferControl("cancel"),
		},
		"transfer-pause": {
			description: "Pause a transfer. What it copied stays, and transfer-resume goes on from there.",
			params:      []verbParam{idParam},
			returns:     rowReturn,
			examples:    []string{`{"id":1,"verb":"transfer-pause","params":{"id":"3f9a1c2b7d00"}}`},
			handler:     transferControl("pause"),
		},
		"transfer-resume": {
			description: "Go on with a paused transfer, try a waiting one now, or try a failed one again from where it stopped.",
			params:      []verbParam{idParam},
			returns:     rowReturn,
			examples:    []string{`{"id":1,"verb":"transfer-resume","params":{"id":"3f9a1c2b7d00"}}`},
			handler:     transferControl("resume"),
		},
	}
}
