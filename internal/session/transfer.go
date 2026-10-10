package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
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
// and only then is it renamed into place. Both hashes are taken while the
// bytes move: the machine that reads the file hashes what it reads, and the
// machine that writes the part hashes what it writes, so a copy reads each
// file once on each side. A link that drops leaves the part. The job waits
// for the machine, then compares the last MiB of the part with the same range
// of the source and goes on from the part's end when they match. A resumed
// file is hashed whole at the end on both sides.
//
// A folder's small files go in one tree stream (transfer_tree.go) instead of
// a few round trips each, and a file over treeSmallMax goes by itself, so it
// can resume from the middle. Before a folder copy into a folder that is
// there, a check-ahead pass asks the far side about every file in batches and
// leaves out each one that is there already with the same sha256.
//
// Each job writes parts of its own, named by its id, so two jobs to one path
// never write one part. The job is also kept in a journal on disk
// (transfer_journal.go), so a daemon that restarts finds its jobs and goes on
// with them from their parts, and every change of a job is an event on the
// daemon's event stream.

// transferMaxRunning bounds the jobs that move bytes at once. The rest wait
// in order.
const transferMaxRunning = 3

// transferKeepDone is how long a finished job stays in the list.
const transferKeepDone = 30 * time.Minute

// transferTailCheck is the range a resume compares before it trusts a part.
const transferTailCheck = 1 << 20

// transferHashTimeout bounds one hash of a whole file on another machine.
const transferHashTimeout = 30 * time.Minute

// transferConflictsShown bounds the conflicts a row lists at once. The next
// ones show when these are answered.
const transferConflictsShown = 200

// ErrVerbSourceChanged is a file that changed on its machine while it was
// copied: its size or modification time is not what the copy started from.
const ErrVerbSourceChanged = "source_changed"

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
	// transferConflict is a copy that waits for an answer to files that are
	// there and differ (transfer-answer).
	transferConflict  = "conflict"
	transferDone      = "done"
	transferFailed    = "failed"
	transferCancelled = "cancelled"
)

// transferOptions is how a job copies, fixed when it starts.
type transferOptions struct {
	Move bool
	// Conflict is what to do with a destination that is there: fail (or
	// empty), replace, keep-both, merge (a folder into the folder there),
	// skip, or ask.
	Conflict string
	// Each is what a folder copy does with each file that is there and
	// differs: replace, keep-both, skip or ask. Empty follows Conflict.
	Each string
	// Place is how the destination is read: empty for the full path of the
	// copy, auto for the cp rule (a folder that is there gets the copy
	// inside it), into for a folder that must be there.
	Place string
	// Label names the copy keep-both makes: "name (LABEL).ext".
	Label string
	// Private keeps every file it writes owner only, as a drop folder's are,
	// instead of taking the original's permission bits.
	Private    bool
	NoPerms    bool
	NoTimes    bool
	NoCompress bool
	// RateLimit caps the bytes a second, 0 for none.
	RateLimit int64
	// Pane and PaneSession name the pane that started the copy, for a copy
	// started from a pane.
	Pane, PaneSession string
}

// transferJob is one copy.
type transferJob struct {
	id       string
	src, dst Endpoint
	opts     transferOptions
	created  time.Time

	done atomic.Int64
	// wire counts the bytes that crossed a link, which compression makes
	// fewer than done.
	wire atomic.Int64

	mu        sync.Mutex
	state     string
	size      int64
	isDir     bool
	files     int
	filesDone int
	finished  map[string]bool
	// target is where the copy goes, as the first attempt read the
	// destination: the file's path, or the folder's.
	target    string
	current   string
	curBase   int64
	curSize   int64
	final     string
	hash      string
	verified  bool
	errText   string
	errCode   string
	resumedAt int64
	resumes   int
	restarts  int
	started   time.Time
	ended     time.Time
	samples   []rateSample
	// skipped names what a folder copy did not carry (links, pipes, sockets,
	// devices), and skippedCount counts all of it.
	skipped      []SkippedItem
	skippedCount int
	// same counts the files that were there already with the same bytes,
	// conflictsSkipped the files a skip answer left out, conflictsLeft the
	// files that appeared during the copy under a policy that asks, and
	// failedFiles the files that did not copy.
	same             int
	conflictsSkipped int
	conflictsLeft    int
	failedFiles      int
	// pending is the conflicts that wait for an answer, answers what the
	// answers said by file, and answerAll the answer for every file.
	pending   []ConflictItem
	answers   map[string]string
	answerAll string
	answered  chan struct{}
	// emitted is the state the last transfer event reported, and
	// lastProgress when the last progress event went, so a copy says each
	// state once and its progress at most four times a second.
	emitted      string
	lastProgress time.Time
	// saved is when the journal last took the list of finished files.
	saved   time.Time
	cancel  context.CancelFunc
	stop    string // "pause" or "cancel" while an attempt is being stopped
	wake    chan struct{}
	retryAt time.Time
	limiter rateLimiter
}

type rateSample struct {
	at   time.Time
	done int64
}

// ConflictItem is a file a copy found at its destination with other bytes.
type ConflictItem struct {
	Rel      string `json:"rel"`
	Path     string `json:"path"`
	SrcSize  int64  `json:"src_size"`
	SrcMTime int64  `json:"src_mtime,omitempty"`
	DstSize  int64  `json:"dst_size"`
	DstMTime int64  `json:"dst_mtime,omitempty"`
}

// transferManager holds the jobs.
type transferManager struct {
	d *Daemon
	// parts is the folders this daemon wrote part files in.
	parts *partDirs
	// sums is the hashes write streams took as their bytes landed.
	sums    partSums
	mu      sync.Mutex
	jobs    map[string]*transferJob
	order   []string
	running chan struct{}
}

func newTransferManager(d *Daemon) *transferManager {
	return &transferManager{d: d, parts: newPartDirs(), jobs: map[string]*transferJob{}, running: make(chan struct{}, transferMaxRunning)}
}

func newTransferID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// errTransferBusy is a copy to a path another copy is writing.
var errTransferBusy = errors.New("busy")

func newJob(id string, src, dst Endpoint, o transferOptions) *transferJob {
	return &transferJob{
		id:       id,
		src:      src,
		dst:      dst,
		opts:     o,
		created:  time.Now(),
		state:    transferQueued,
		finished: map[string]bool{},
		answers:  map[string]string{},
		answered: make(chan struct{}, 1),
		wake:     make(chan struct{}, 1),
	}
}

// start makes a job and runs it in the background. A copy to the same place
// as a copy that has not ended is refused: one of the two would replace the
// other's file.
func (m *transferManager) start(src, dst Endpoint, o transferOptions) (*transferJob, error) {
	j := newJob(newTransferID(), src, dst, o)
	m.mu.Lock()
	for _, id := range m.order {
		other := m.jobs[id]
		if other.dst.Host == dst.Host && (other.dst.Path == dst.Path || other.getTarget() == dst.Path) && !transferEnded(other.getState()) {
			m.mu.Unlock()
			return nil, fmt.Errorf("%w: copy %s writes %s", errTransferBusy, other.id, dst)
		}
	}
	m.jobs[j.id] = j
	m.order = append(m.order, j.id)
	m.pruneLocked()
	m.mu.Unlock()
	from := "the person"
	if o.Pane != "" {
		from = "pane " + shortWindowID(o.Pane)
	}
	LogBasic("Transfer %s starts for %s: %s to %s (move %v, conflict %q, each %q)", j.id, from, src, dst, o.Move, o.Conflict, o.Each)
	m.note(j, true)
	go m.run(j)
	return j, nil
}

// transferEnded reports whether a state is one a job does not leave by
// itself.
func transferEnded(state string) bool {
	return state == transferDone || state == transferFailed || state == transferCancelled
}

func (m *transferManager) get(id string) *transferJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobs[id]
}

// pruneLocked drops finished jobs older than transferKeepDone. A failed job
// that leaves the list takes its parts and its journal entry with it: nothing
// can resume it after that.
func (m *transferManager) pruneLocked() {
	m.dropLocked(func(j *transferJob) bool {
		return !j.ended.IsZero() && time.Since(j.ended) > transferKeepDone
	})
}

// dropLocked removes the jobs gone says to, with their journal entries and
// the parts of the failed ones. gone is called with j.mu held.
func (m *transferManager) dropLocked(gone func(j *transferJob) bool) int {
	keep := m.order[:0]
	n := 0
	for _, id := range m.order {
		j := m.jobs[id]
		j.mu.Lock()
		drop := gone(j)
		failed := j.state == transferFailed
		j.mu.Unlock()
		if drop {
			delete(m.jobs, id)
			m.forget(j)
			if failed {
				go m.abortParts(j)
			}
			n++
			continue
		}
		keep = append(keep, id)
	}
	m.order = keep
	return n
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

func (j *transferJob) getTarget() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.target
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
			j.limiter = rateLimiter{rate: j.opts.RateLimit}
		})
		if held {
			cancel()
			<-m.running
			continue
		}
		m.note(j, false)
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
				j.current = ""
			})
			LogBasic("Transfer %s finished: %s to %s, %d bytes, %d over the links", j.id, j.src, j.dst, j.done.Load(), j.wire.Load())
			m.note(j, false)
			return
		case stop == "cancel":
			m.abortParts(j)
			j.set(func(j *transferJob) {
				j.state = transferCancelled
				j.ended = time.Now()
			})
			LogBasic("Transfer %s cancelled: %s to %s", j.id, j.src, j.dst)
			m.note(j, false)
			return
		case stop == "pause":
			j.set(func(j *transferJob) { j.state = transferPaused })
			m.note(j, false)
			tries = 0
			continue
		case ctx.Err() != nil:
			// The daemon is stopping. The journal keeps the job, and the
			// next start goes on with it.
			return
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
				if te.code == ErrVerbHashMismatch {
					j.errText = "The copy did not match the original twice. The disk or the file may be changing. " + te.msg
				}
				j.ended = time.Now()
			})
			LogBasic("Transfer %s failed: %s", j.id, te.msg)
			m.note(j, false)
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
		m.note(j, false)
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
	src := fileEnd{d: m.d, host: j.src.Host, wire: &j.wire}
	dst := fileEnd{d: m.d, host: j.dst.Host, wire: &j.wire}

	info, err := src.stat(ctx, j.src.Path)
	if err != nil {
		return classify(err)
	}
	if !info.exists {
		return permanent(ErrVerbNoFile, j.src.String()+" does not exist")
	}
	target, err := m.resolveTarget(ctx, j, dst, info)
	if err != nil {
		return classify(err)
	}
	if j.opts.Private || j.opts.NoPerms {
		info.perm = 0
	}
	if j.opts.NoTimes {
		info.mtime = 0
	}
	if !info.isDir {
		return m.attemptFile(ctx, j, src, dst, info, target)
	}
	return m.attemptDir(ctx, j, src, dst, info, target)
}

// resolveTarget reads the destination once, on the first attempt: with place
// auto a folder that is there gets the copy inside it, as cp does, and with
// place into it must be a folder. Later attempts keep the answer, so a
// folder the first attempt made does not move the copy into itself.
func (m *transferManager) resolveTarget(ctx context.Context, j *transferJob, dst fileEnd, info statResult) (string, error) {
	if t := j.getTarget(); t != "" {
		return t, nil
	}
	target := j.dst.Path
	place := j.opts.Place
	if place == "contents" && !info.isDir {
		place = "auto"
	}
	if place == "auto" || place == "into" {
		st, err := dst.stat(ctx, target)
		if err != nil {
			return "", err
		}
		switch {
		case st.exists && st.isDir:
			target = joinRemote(target, path.Base(filepath.ToSlash(j.src.Path)))
		case place == "into" && !st.exists:
			return "", permanent(ErrVerbNoFile, "the folder "+j.dst.String()+" does not exist")
		case place == "into":
			return "", permanent(ErrVerbInvalidParams, j.dst.String()+" is not a folder")
		}
	}
	if j.src.Host == j.dst.Host {
		s, t := filepath.Clean(j.src.Path), filepath.Clean(target)
		if s == t {
			return "", permanent(ErrVerbInvalidParams, "the copy would land on the file it copies: "+j.src.String())
		}
		if info.isDir && pathUnder(t, s) {
			return "", permanent(ErrVerbInvalidParams, "a folder cannot be copied into itself: "+j.dst.String())
		}
	}
	// Another copy that writes the same place would replace this one's
	// files, or this one its.
	m.mu.Lock()
	for _, id := range m.order {
		o := m.jobs[id]
		if o != j && o.dst.Host == j.dst.Host && o.getTarget() == target && !transferEnded(o.getState()) {
			m.mu.Unlock()
			return "", permanent(ErrVerbBusy, "another copy writes "+target+" now: "+o.id)
		}
	}
	m.mu.Unlock()
	j.set(func(j *transferJob) { j.target = target })
	return target, nil
}

// keepAction is what a file that is not there yet gets at its commit, should
// one appear there during the copy: a file made meanwhile is not replaced
// unless the policy says to.
func keepAction(policy string) string {
	switch policy {
	case "replace", "keep-both", "skip":
		return policy
	case "ask":
		return "skip"
	}
	return "fail"
}

// attemptFile copies one file.
func (m *transferManager) attemptFile(ctx context.Context, j *transferJob, src, dst fileEnd, info statResult, target string) error {
	name := path.Base(filepath.ToSlash(j.src.Path))
	j.set(func(j *transferJob) {
		j.size, j.files, j.isDir = info.size, 1, false
		j.current = name
	})
	existing, err := dst.stat(ctx, target)
	if err != nil {
		return classify(err)
	}
	policy := j.opts.Conflict
	if policy == "merge" {
		// A file has nothing to merge: each says what to do with it.
		policy = j.opts.Each
	}
	action := keepAction(policy)
	if existing.exists {
		if existing.isDir {
			return permanent(ErrVerbFileExists, j.dst.Host+":"+target+" is a folder, so the file cannot go there")
		}
		if existing.size == info.size {
			same, sum, err := m.sameFile(ctx, src, dst, j.src.Path, target)
			if err != nil {
				return classify(err)
			}
			if same {
				// A file that is there with the same bytes is the copy
				// already. Nothing is written.
				j.set(func(j *transferJob) {
					j.same, j.filesDone, j.hash, j.verified, j.final = 1, 1, sum, true, target
				})
				j.done.Store(info.size)
				m.fileEvent(j, name, "same", sum, "", "")
				if j.opts.Move {
					if err := src.remove(ctx, j.src.Path, false); err != nil {
						return classify(err)
					}
				}
				return nil
			}
		}
		switch policy {
		case "replace", "keep-both":
			action = policy
		case "skip":
			j.set(func(j *transferJob) { j.conflictsSkipped = 1 })
			m.fileEvent(j, name, "skipped", "", ErrVerbFileExists, "")
			return nil
		case "ask":
			got, err := m.ask(ctx, j, []ConflictItem{{Rel: name, Path: target, SrcSize: info.size, SrcMTime: info.mtime, DstSize: existing.size, DstMTime: existing.mtime}})
			if err != nil {
				return err
			}
			action = got[name]
			if action == "skip" {
				j.set(func(j *transferJob) { j.conflictsSkipped = 1 })
				m.fileEvent(j, name, "skipped", "", ErrVerbFileExists, "")
				return nil
			}
		default:
			return permanent(ErrVerbFileExists, j.dst.Host+":"+target+" already exists")
		}
	}
	got, skipped, sum, err := m.copyFile(ctx, j, src, dst, fileCopy{
		rel: name, from: j.src.Path, to: target, size: info.size, conflict: action,
		perm: info.perm, mtime: info.mtime, srcMTime: info.mtime,
	})
	if err != nil {
		return classify(err)
	}
	if skipped {
		// A file appeared at the destination during the copy.
		j.set(func(j *transferJob) {
			if policy == "ask" {
				j.conflictsLeft = 1
			} else {
				j.conflictsSkipped = 1
			}
		})
		m.fileEvent(j, name, "conflict", "", ErrVerbFileExists, "")
		return nil
	}
	j.set(func(j *transferJob) { j.final = got; j.filesDone = 1 })
	m.fileEvent(j, name, "done", sum, "", "")
	if j.opts.Move {
		if err := src.remove(ctx, j.src.Path, false); err != nil {
			return classify(err)
		}
	}
	return nil
}

// sameFile reports whether two files hold the same bytes, by their sha256,
// each taken on its own machine at once.
func (m *transferManager) sameFile(ctx context.Context, src, dst fileEnd, a, b string) (bool, string, error) {
	type res struct {
		sum string
		err error
	}
	theirs := make(chan res, 1)
	go func() {
		sum, err := dst.hash(ctx, b, 0, -1)
		theirs <- res{sum, err}
	}()
	ours, err := src.hash(ctx, a, 0, -1)
	t := <-theirs
	if err != nil {
		return false, "", err
	}
	if t.err != nil {
		return false, "", t.err
	}
	return ours == t.sum, ours, nil
}

// fileTask is one file of a folder copy and what its commit does when a file
// is there.
type fileTask struct {
	e      WalkEntry
	action string
}

// attemptDir copies a folder.
func (m *transferManager) attemptDir(ctx context.Context, j *transferJob, src, dst fileEnd, info statResult, target string) error {
	j.mu.Lock()
	final := j.final
	j.mu.Unlock()
	// merging is a copy into a folder that may hold files already, which
	// asks the far side about them before it sends any.
	merging := final != ""
	dstPath := target
	if final != "" {
		// A folder copy keeps the folder it chose on its first attempt.
		dstPath = final
	} else {
		existing, err := dst.stat(ctx, dstPath)
		if err != nil {
			return classify(err)
		}
		if existing.exists {
			if !existing.isDir {
				return permanent(ErrVerbFileExists, j.dst.Host+":"+dstPath+" is a file, so the folder cannot go there")
			}
			switch j.opts.Conflict {
			case "keep-both":
				dstPath, err = dst.freeName(ctx, dstPath)
				if err != nil {
					return classify(err)
				}
			case "replace", "merge", "skip", "ask":
				merging = true
			default:
				return permanent(ErrVerbFileExists, j.dst.String()+" already exists")
			}
		}
		j.set(func(j *transferJob) { j.final = dstPath })
	}
	each := j.opts.Each
	if each == "" {
		switch j.opts.Conflict {
		case "skip", "ask":
			each = j.opts.Conflict
		default:
			each = "replace"
		}
	}

	w, err := src.walk(ctx, j.src.Path)
	if err != nil {
		return classify(err)
	}
	entries, total := w.entries, w.total
	for _, s := range w.skipped {
		if !safeRel(s.Rel) {
			return permanent(ErrVerbInvalidParams, j.src.String()+" names an item outside the folder: "+echoName(s.Rel)+". Nothing outside the folder was written.")
		}
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
	j.set(func(j *transferJob) {
		j.size, j.files, j.isDir = total, files, true
		j.skipped, j.skippedCount = w.skipped, w.skippedCount
	})
	if w.skippedCount > 0 {
		LogBasic("Transfer %s does not copy %d items of %s that are not files or folders, such as links", j.id, w.skippedCount, j.src)
	}
	if err := dst.mkdir(ctx, dstPath); err != nil {
		return classify(err)
	}

	// What the finished files hold counts as done from the start, so a resume
	// shows the copy where it is and not from zero.
	var base int64
	var dirs []string
	var todo []WalkEntry
	j.mu.Lock()
	for _, e := range entries {
		switch {
		case e.Dir:
			dirs = append(dirs, e.Rel)
		case j.finished[e.Rel]:
			base += e.Size
		default:
			todo = append(todo, e)
		}
	}
	j.mu.Unlock()
	j.done.Store(base)

	tasks, extra, err := m.checkAhead(ctx, j, src, dst, dstPath, todo, merging, each)
	if err != nil {
		return err
	}
	base += extra
	j.done.Store(base)

	var small, large []fileTask
	for _, t := range tasks {
		if t.e.Size <= treeSmallMax {
			small = append(small, t)
		} else {
			large = append(large, t)
		}
	}
	perm := func(e WalkEntry) uint32 {
		if j.opts.Private || j.opts.NoPerms {
			return 0
		}
		return e.Perm
	}
	mtime := func(e WalkEntry) int64 {
		if j.opts.NoTimes {
			return 0
		}
		return e.MTime
	}

	var failed []treeResult
	if len(small) > 0 || len(dirs) > 0 {
		res, err := m.treeCopy(ctx, j, src, dst, j.src.Path, dstPath, dirs, small, perm, mtime)
		var ve *VerbCallError
		if errors.As(err, &ve) && ve.Code == ErrVerbUnknownVerb {
			// A daemon from before tree streams: each file by itself.
			LogBasic("Transfer %s: a far daemon has no tree stream, so each file goes by itself", j.id)
			for _, d := range dirs {
				if err := dst.mkdir(ctx, joinRemote(dstPath, d)); err != nil {
					return classify(err)
				}
			}
			large = append(small, large...)
			res, err = nil, nil
		}
		if err != nil {
			return classify(err)
		}
		for _, r := range res {
			if r.code == ErrVerbHashMismatch {
				// Once more by itself, which reads it again on both sides.
				for _, t := range small {
					if t.e.Rel == r.rel {
						large = append(large, t)
						break
					}
				}
				continue
			}
			failed = append(failed, r)
		}
	}
	for _, t := range large {
		e := t.e
		fc := fileCopy{
			rel: e.Rel, from: joinRemote(j.src.Path, e.Rel), to: joinRemote(dstPath, e.Rel),
			size: e.Size, base: j.done.Load(), conflict: t.action, perm: perm(e), mtime: mtime(e), srcMTime: e.MTime,
		}
		_, skipped, sum, err := m.copyFile(ctx, j, src, dst, fc)
		if err != nil {
			c := classify(err)
			var te *transferError
			if errors.As(c, &te) && !te.retry && te.code != ErrVerbHashMismatch && ctx.Err() == nil {
				// One file that cannot copy does not stop the others.
				failed = append(failed, treeResult{rel: e.Rel, code: te.code, msg: te.msg})
				m.fileEvent(j, e.Rel, "failed", "", te.code, te.msg)
				continue
			}
			return c
		}
		j.done.Store(fc.base + e.Size)
		m.noteFileResult(j, e.Rel, each, skipped, sum)
	}
	j.done.Store(total)
	if len(failed) > 0 {
		j.set(func(j *transferJob) { j.failedFiles = len(failed) })
		first := failed[0]
		msg := fmt.Sprintf("%d of %d files did not copy. The first: %s", len(failed), files, first.msg)
		if len(failed) == 1 {
			msg = first.msg
		}
		return permanent(first.code, msg)
	}
	if j.opts.Move {
		j.mu.Lock()
		var carried []WalkEntry
		for _, e := range entries {
			if e.Dir || j.finished[e.Rel] {
				carried = append(carried, e)
			}
		}
		j.mu.Unlock()
		left, err := removeMoved(ctx, src, j.src.Path, carried)
		if err != nil {
			return classify(err)
		}
		if left || len(carried) < len(entries) {
			j.set(func(j *transferJob) {
				j.errText = "The copy is done. Some items in " + j.src.String() + " were not copied, such as links, pipes, skipped files or new files, so they stay there."
			})
		}
	}
	return nil
}

// noteFileResult records a file a folder copy finished.
func (m *transferManager) noteFileResult(j *transferJob, rel, each string, skipped bool, sum string) {
	if skipped {
		j.set(func(j *transferJob) {
			if each == "ask" {
				j.conflictsLeft++
			} else {
				j.conflictsSkipped++
			}
		})
		m.fileEvent(j, rel, "conflict", "", ErrVerbFileExists, "")
		return
	}
	j.set(func(j *transferJob) {
		j.finished[rel] = true
		j.filesDone = len(j.finished)
	})
	m.fileEvent(j, rel, "done", sum, "", "")
	m.saveFinished(j)
}

// checkAhead decides each file of a folder copy before any byte moves. Into a
// folder that holds nothing of the copy yet, every file goes. Into one that
// may hold some, the far side is asked about all of them in batches: a file
// there with the same size is hashed on both machines, and one with the same
// sha256 is left out as the copy already. One that differs follows each, and
// ask waits for the answers. It returns the files to send and the bytes of
// those left out, for the progress.
func (m *transferManager) checkAhead(ctx context.Context, j *transferJob, src, dst fileEnd, dstPath string, todo []WalkEntry, merging bool, each string) ([]fileTask, int64, error) {
	tasks := make([]fileTask, 0, len(todo))
	if !merging {
		for _, e := range todo {
			tasks = append(tasks, fileTask{e: e, action: keepAction(each)})
		}
		return tasks, 0, nil
	}
	rels := make([]string, len(todo))
	for i, e := range todo {
		rels[i] = e.Rel
	}
	there, err := dst.check(ctx, dstPath, rels, false)
	if err != nil {
		return nil, 0, classify(err)
	}
	var sameSize []string
	for i, e := range todo {
		if t := there[i]; t.Exists && t.Kind == "file" && t.Size == e.Size {
			sameSize = append(sameSize, e.Rel)
		}
	}
	srcSums, dstSums := map[string]string{}, map[string]string{}
	if len(sameSize) > 0 {
		type res struct {
			out []FileCheck
			err error
		}
		theirs := make(chan res, 1)
		go func() {
			out, err := dst.check(ctx, dstPath, sameSize, true)
			theirs <- res{out, err}
		}()
		ours, err := src.check(ctx, j.src.Path, sameSize, true)
		t := <-theirs
		if err != nil {
			return nil, 0, classify(err)
		}
		if t.err != nil {
			return nil, 0, classify(t.err)
		}
		for i, rel := range sameSize {
			if i < len(ours) {
				srcSums[rel] = ours[i].SHA256
			}
			if i < len(t.out) {
				dstSums[rel] = t.out[i].SHA256
			}
		}
	}
	var extra int64
	var asks []ConflictItem
	conflicted := map[string]WalkEntry{}
	for i, e := range todo {
		t := there[i]
		switch {
		case !t.Exists:
			tasks = append(tasks, fileTask{e: e, action: keepAction(each)})
			continue
		case t.Kind != "file":
			// A folder or a link where the file goes. It is not replaced.
			extra += e.Size
			j.set(func(j *transferJob) { j.conflictsLeft++ })
			m.fileEvent(j, e.Rel, "conflict", "", ErrVerbFileExists, "a "+t.Kind+" is in the way")
			continue
		}
		if sum := srcSums[e.Rel]; sum != "" && sum == dstSums[e.Rel] {
			extra += e.Size
			j.set(func(j *transferJob) {
				j.same++
				j.finished[e.Rel] = true
				j.filesDone = len(j.finished)
			})
			m.fileEvent(j, e.Rel, "same", sum, "", "")
			continue
		}
		switch each {
		case "replace", "keep-both":
			tasks = append(tasks, fileTask{e: e, action: each})
		case "skip":
			extra += e.Size
			j.set(func(j *transferJob) { j.conflictsSkipped++ })
			m.fileEvent(j, e.Rel, "skipped", "", ErrVerbFileExists, "")
		default:
			conflicted[e.Rel] = e
			asks = append(asks, ConflictItem{Rel: e.Rel, Path: joinRemote(dstPath, e.Rel), SrcSize: e.Size, SrcMTime: e.MTime, DstSize: t.Size, DstMTime: t.MTime})
		}
	}
	m.saveFinished(j)
	if len(asks) > 0 {
		got, err := m.ask(ctx, j, asks)
		if err != nil {
			return nil, 0, err
		}
		for _, a := range asks {
			e := conflicted[a.Rel]
			switch choice := got[a.Rel]; choice {
			case "replace", "keep-both":
				tasks = append(tasks, fileTask{e: e, action: choice})
			default:
				extra += e.Size
				j.set(func(j *transferJob) { j.conflictsSkipped++ })
				m.fileEvent(j, e.Rel, "skipped", "", ErrVerbFileExists, "")
			}
		}
	}
	return tasks, extra, nil
}

// ask puts conflicts to whoever follows the copy and waits for an answer to
// each: the job is in state conflict until then, and transfer-answer answers.
// An answer given before, for a file or for all, stands. It returns the
// answer per file.
func (m *transferManager) ask(ctx context.Context, j *transferJob, items []ConflictItem) (map[string]string, error) {
	out := make(map[string]string, len(items))
	for {
		j.mu.Lock()
		var pending []ConflictItem
		for _, it := range items {
			switch {
			case j.answers[it.Rel] != "":
				out[it.Rel] = j.answers[it.Rel]
			case j.answerAll != "":
				out[it.Rel] = j.answerAll
			default:
				pending = append(pending, it)
			}
		}
		if len(pending) == 0 {
			wasConflict := j.state == transferConflict
			j.pending = nil
			if wasConflict {
				j.state = transferRunning
			}
			j.mu.Unlock()
			if wasConflict {
				m.note(j, false)
			}
			return out, nil
		}
		j.pending = pending
		was := j.state
		j.state = transferConflict
		j.mu.Unlock()
		if was != transferConflict {
			LogBasic("Transfer %s waits for an answer about %d files that are there and differ", j.id, len(pending))
			m.note(j, false)
		}
		select {
		case <-j.answered:
		case <-ctx.Done():
			j.set(func(j *transferJob) {
				if j.state == transferConflict {
					j.state = transferRunning
				}
			})
			return nil, ctx.Err()
		}
	}
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

// fileCopy is one file a copy sends by itself.
type fileCopy struct {
	rel      string
	from, to string
	size     int64
	// base is what the job had done before this file, for the progress.
	base     int64
	conflict string
	label    string
	perm     uint32
	mtime    int64
	// srcMTime is the source's modification time the copy started from, so
	// a file that changes during the copy is found. 0 skips the check.
	srcMTime int64
}

// emptySHA256 is the sha256 of no bytes.
var emptySHA256 = func() string { s := sha256.Sum256(nil); return hex.EncodeToString(s[:]) }()

// copyFile copies one file through its part, resuming a part that is there,
// and returns where the file ended up, whether a skip conflict left it out,
// and its sha256.
//
// From the start of a file, both hashes are taken on the way: the reading
// side hashes what it reads (this daemon for its own disk, the far daemon in
// the stream's trailer), and the writing side hashes what it writes. A resume
// has bytes from an earlier attempt in its part, so it hashes the source
// whole beside the copy, and the commit reads the part whole.
func (m *transferManager) copyFile(ctx context.Context, j *transferJob, src, dst fileEnd, fc fileCopy) (string, bool, string, error) {
	part, err := dst.partSize(ctx, fc.to, j.id)
	if err != nil {
		return "", false, "", err
	}
	size := fc.size
	offset := int64(0)
	if part > 0 && part <= size {
		n := min(part, int64(transferTailCheck))
		a, aerr := src.hash(ctx, fc.from, part-n, n)
		b, berr := dst.partHash(ctx, fc.to, j.id, part-n, n)
		if aerr != nil {
			return "", false, "", aerr
		}
		if berr == nil && a == b {
			offset = part
		}
	}
	type hashed struct {
		sum string
		err error
	}
	var srcHash chan hashed
	if offset > 0 {
		j.set(func(j *transferJob) {
			j.resumedAt = fc.base + offset
			j.resumes++
		})
		LogBasic("Transfer %s resumes %s at byte %d of %d", j.id, fc.from, offset, size)
		srcHash = make(chan hashed, 1)
		go func() {
			hctx, cancel := context.WithTimeout(ctx, transferHashTimeout)
			defer cancel()
			sum, err := src.hash(hctx, fc.from, 0, -1)
			srcHash <- hashed{sum, err}
		}()
	}
	j.set(func(j *transferJob) {
		j.current, j.curBase, j.curSize = fc.rel, fc.base, size
	})
	j.done.Store(fc.base + offset)
	j.sample(true)

	sum := ""
	known := ""
	if offset < size || size == 0 {
		in, err := src.openRead(ctx, fc.from, offset, !src.local() && j.compressionWanted() && compressibleName(fc.from))
		if err != nil {
			return "", false, "", err
		}
		if fc.srcMTime > 0 && (in.size != size || in.mtime != fc.srcMTime) {
			_ = in.Close()
			return "", false, "", permanent(ErrVerbSourceChanged, echoName(fc.from)+" changed while it was copied. Copy it again when it is not being written.")
		}
		zOut := false
		if !dst.local() && j.compressionWanted() {
			if f, ok := in.r.(*os.File); ok {
				zOut = compressibleFile(f, fc.from, size-offset, offset)
			} else {
				zOut = in.compressed
			}
		}
		w, finish, err := dst.openWrite(ctx, fc.to, j.id, offset, size-offset, zOut)
		if err != nil {
			_ = in.Close()
			return "", false, "", err
		}
		stop := context.AfterFunc(ctx, func() {
			_ = in.Close()
			_ = w.Close()
		})
		h := sha256.New()
		var body io.Reader = io.LimitReader(in, size-offset)
		if offset == 0 {
			body = io.TeeReader(body, h)
		}
		var out io.Writer = w
		var fw interface {
			io.Writer
			Close() error
		}
		if zOut {
			f := getFlateWriter(w)
			fw, out = f, f
			defer putFlateWriter(f)
		}
		pw := &progressWriter{w: out, j: j, m: m, ctx: ctx}
		n, cerr := io.CopyBuffer(pw, body, make([]byte, 256<<10))
		if cerr == nil && fw != nil {
			cerr = fw.Close()
		}
		if cerr == nil && n < size-offset {
			cerr = io.ErrUnexpectedEOF
		}
		var theirs string
		if cerr == nil && offset == 0 {
			theirs, cerr = in.trailer()
		}
		stop()
		_ = in.Close()
		if cerr != nil {
			_ = w.Close()
			if ctx.Err() != nil {
				return "", false, "", ctx.Err()
			}
			return "", false, "", cerr
		}
		if err := finish(); err != nil {
			return "", false, "", err
		}
		if offset == 0 {
			sum = hex.EncodeToString(h.Sum(nil))
			if theirs != "" && theirs != sum {
				_ = dst.abort(ctx, fc.to, j.id)
				return "", false, "", permanent(ErrVerbHashMismatch, "the bytes of "+echoName(fc.rel)+" that arrived are not the bytes "+src.name()+" read")
			}
			if dst.local() {
				// This daemon wrote the part, so its hash is the one taken
				// on the way.
				known = sum
			}
		}
	}

	j.set(func(j *transferJob) {
		j.state = transferVerifying
		j.done.Store(fc.base + size)
	})
	m.note(j, false)
	if srcHash != nil {
		var h hashed
		select {
		case h = <-srcHash:
		case <-ctx.Done():
			return "", false, "", ctx.Err()
		}
		if h.err != nil {
			return "", false, "", h.err
		}
		sum = h.sum
	}
	if sum == "" {
		sum = emptySHA256
	}
	if fc.srcMTime > 0 && size > treeSmallMax {
		// A large file takes long enough to copy that its writer may have
		// come back to it.
		st, err := src.stat(ctx, fc.from)
		if err != nil {
			return "", false, "", err
		}
		if st.size != size || st.mtime != fc.srcMTime {
			_ = dst.abort(ctx, fc.to, j.id)
			return "", false, "", permanent(ErrVerbSourceChanged, echoName(fc.from)+" changed while it was copied. Copy it again when it is not being written.")
		}
	}
	label := fc.label
	if label == "" {
		label = j.opts.Label
	}
	got, skipped, err := dst.commit(ctx, fc.to, j.id, sum, fc.conflict, fc.perm, fc.mtime, label, known)
	if err != nil {
		return "", false, "", err
	}
	j.set(func(j *transferJob) {
		j.hash, j.verified = sum, true
		if j.state == transferVerifying {
			j.state = transferRunning
		}
	})
	m.note(j, false)
	return got, skipped, sum, nil
}

// progressWriter counts the bytes a copy has written, and holds them to the
// copy's rate limit.
type progressWriter struct {
	w   io.Writer
	j   *transferJob
	m   *transferManager
	ctx context.Context
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.j.moved(p.ctx, p.m, n)
	return n, err
}

// moved counts n bytes the copy moved: the progress, the rate, and the wait
// a rate limit asks for.
func (j *transferJob) moved(ctx context.Context, m *transferManager, n int) {
	j.done.Add(int64(n))
	j.sample(false)
	m.progress(j)
	j.limiter.wait(ctx, n)
}

// rateLimiter holds a copy to a rate: after each write it sleeps until the
// bytes so far are no more than the rate allows since the first.
type rateLimiter struct {
	rate  int64
	start time.Time
	sent  int64
}

func (l *rateLimiter) wait(ctx context.Context, n int) {
	if l.rate <= 0 {
		return
	}
	if l.start.IsZero() {
		l.start = time.Now()
	}
	l.sent += int64(n)
	due := l.start.Add(time.Duration(float64(l.sent) / float64(l.rate) * float64(time.Second)))
	if d := time.Until(due); d > 0 {
		t := time.NewTimer(d)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
		}
	}
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
	for cut < len(j.samples)-1 && now.Sub(j.samples[cut].at) > 6*time.Second {
		cut++
	}
	j.samples = j.samples[cut:]
}

// rateLocked is the bytes per second over the last five seconds or so.
func (j *transferJob) rateLocked() float64 {
	n := len(j.samples)
	if n < 2 {
		return 0
	}
	last := j.samples[n-1]
	first := j.samples[0]
	for _, s := range j.samples {
		if last.at.Sub(s.at) <= 5*time.Second {
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
	isDir, final, current, target := j.isDir, j.final, j.current, j.target
	j.mu.Unlock()
	if target == "" {
		target = j.dst.Path
	}
	if isDir && final != "" && current != "" {
		target = joinRemote(final, current)
	}
	_ = dst.abort(ctx, target, j.id)
}

// TransferRow is one job in transfer-list.
type TransferRow struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Src   Endpoint `json:"src"`
	Dst   Endpoint `json:"dst"`
	Final string   `json:"final,omitempty"`
	// Target is where the copy goes, once the first attempt read the
	// destination.
	Target string `json:"target,omitempty"`
	Kind   string `json:"kind"`
	Move   bool   `json:"move,omitempty"`
	State  string `json:"state"`
	Size   int64  `json:"size"`
	Done   int64  `json:"done"`
	// Wire is the bytes that crossed a link, fewer than Done when they were
	// compressed.
	Wire      int64   `json:"wire,omitempty"`
	Rate      float64 `json:"rate"`
	ETAms     int64   `json:"eta_ms,omitempty"`
	Files     int     `json:"files"`
	FilesDone int     `json:"files_done"`
	Current   string  `json:"current,omitempty"`
	// CurrentDone and CurrentSize are the bytes of the file in flight.
	CurrentDone int64  `json:"current_done,omitempty"`
	CurrentSize int64  `json:"current_size,omitempty"`
	Error       string `json:"error,omitempty"`
	Code        string `json:"code,omitempty"`
	ResumedFrom int64  `json:"resumed_from,omitempty"`
	Resumes     int    `json:"resumes,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Verified    bool   `json:"verified,omitempty"`
	Created     int64  `json:"created"`
	Started     int64  `json:"started,omitempty"`
	Ended       int64  `json:"ended,omitempty"`
	RetryInMs   int64  `json:"retry_in_ms,omitempty"`
	// Skipped counts what a folder copy did not carry: links, pipes,
	// sockets and devices. SkippedItems names the first of them.
	Skipped      int           `json:"skipped,omitempty"`
	SkippedItems []SkippedItem `json:"skipped_items,omitempty"`
	// Same counts the files that were there with the same bytes, which were
	// not copied again.
	Same int `json:"same,omitempty"`
	// ConflictsSkipped counts the files that were there and differed, which
	// skip left as they were. ConflictsLeft counts those that appeared
	// during a copy that asks, which were left too.
	ConflictsSkipped int `json:"conflicts_skipped,omitempty"`
	ConflictsLeft    int `json:"conflicts_left,omitempty"`
	// Conflicts is the files that wait for an answer in state conflict, the
	// first of ConflictCount.
	Conflicts     []ConflictItem `json:"conflicts,omitempty"`
	ConflictCount int            `json:"conflict_count,omitempty"`
	// FailedFiles counts the files of a folder copy that did not copy.
	FailedFiles int `json:"failed_files,omitempty"`
	// Pane names the pane that started the copy, and PaneSession its
	// session.
	Pane        string `json:"pane,omitempty"`
	PaneSession string `json:"pane_session,omitempty"`
}

func (j *transferJob) row() TransferRow {
	j.mu.Lock()
	defer j.mu.Unlock()
	r := TransferRow{
		ID: j.id, Name: path.Base(filepath.ToSlash(j.src.Path)), Src: j.src, Dst: j.dst, Final: j.final, Target: j.target,
		Kind: "file", Move: j.opts.Move, State: j.state, Size: j.size, Done: j.done.Load(), Wire: j.wire.Load(),
		Files: j.files, FilesDone: j.filesDone, Current: j.current,
		Error: j.errText, Code: j.errCode, ResumedFrom: j.resumedAt, Resumes: j.resumes,
		SHA256: j.hash, Verified: j.verified, Created: j.created.UnixMilli(),
		Skipped: j.skippedCount, SkippedItems: j.skipped,
		Same: j.same, ConflictsSkipped: j.conflictsSkipped, ConflictsLeft: j.conflictsLeft,
		ConflictCount: len(j.pending), FailedFiles: j.failedFiles,
		Pane: j.opts.Pane, PaneSession: j.opts.PaneSession,
	}
	if j.isDir {
		r.Kind = "dir"
		r.SHA256, r.Verified = "", j.state == transferDone && j.failedFiles == 0
	}
	if len(j.pending) > 0 {
		r.Conflicts = j.pending[:min(len(j.pending), transferConflictsShown)]
	}
	if j.current != "" && j.curSize > 0 && !transferEnded(j.state) {
		r.CurrentSize = j.curSize
		r.CurrentDone = min(max(r.Done-j.curBase, 0), j.curSize)
	}
	if j.state == transferRunning || j.state == transferVerifying {
		r.Rate = j.rateLocked()
		// Time left after two seconds of data, so the first guess is not a
		// wild one.
		if r.Rate > 0 && r.Size > r.Done && len(j.samples) > 0 && time.Since(j.samples[0].at) >= 2*time.Second {
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

func (m *transferManager) jobsNewestFirst() []*transferJob {
	m.mu.Lock()
	m.pruneLocked()
	jobs := make([]*transferJob, 0, len(m.order))
	for _, id := range m.order {
		jobs = append(jobs, m.jobs[id])
	}
	m.mu.Unlock()
	sort.SliceStable(jobs, func(a, b int) bool { return jobs[a].created.After(jobs[b].created) })
	return jobs
}

func (m *transferManager) rows() []TransferRow {
	jobs := m.jobsNewestFirst()
	out := make([]TransferRow, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.row())
	}
	return out
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
	if strings.ContainsRune(e.Path, 0) {
		return invalidParam(name+".path", "a path cannot hold a NUL byte")
	}
	return d.checkHostParam(e.Host)
}

// thisMachineLabel is the name a keep-both copy from this machine carries on
// another: the host name up to its first dot.
func thisMachineLabel() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "another machine"
	}
	h, _, _ = strings.Cut(h, ".")
	if checkKeepLabel("from "+h) != nil {
		return "another machine"
	}
	return h
}

func (d *Daemon) verbTransferStart(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Src        Endpoint `json:"src"`
		Dst        Endpoint `json:"dst"`
		Move       bool     `json:"move"`
		Conflict   string   `json:"conflict"`
		Each       string   `json:"each"`
		Place      string   `json:"place"`
		Perms      *bool    `json:"perms"`
		Times      *bool    `json:"times"`
		Compress   *bool    `json:"compress"`
		RateLimit  int64    `json:"rate_limit"`
		KeepSuffix *bool    `json:"keep_from"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Place == "auto" && strings.HasSuffix(p.Dst.Path, "/") {
		// A destination written with a slash at its end is a folder, as cp
		// reads it.
		p.Place = "into"
	}
	if verr := d.checkEndpoint("src", &p.Src); verr != nil {
		return nil, verr
	}
	if verr := d.checkEndpoint("dst", &p.Dst); verr != nil {
		return nil, verr
	}
	switch p.Conflict {
	case "", "fail", "replace", "keep-both", "merge", "skip", "ask":
	default:
		return nil, invalidParam("conflict", "conflict is replace, keep-both, merge, skip, ask or fail", "replace", "keep-both", "merge", "skip", "ask", "fail")
	}
	switch p.Each {
	case "", "replace", "keep-both", "skip", "ask":
	default:
		return nil, invalidParam("each", "each is replace, keep-both, skip or ask", "replace", "keep-both", "skip", "ask")
	}
	switch p.Place {
	case "", "auto", "into", "contents":
	default:
		return nil, invalidParam("place", "place is auto, into or contents, or omit it for the full path of the copy", "auto", "into", "contents")
	}
	if p.RateLimit < 0 {
		return nil, invalidParam("rate_limit", "rate_limit is bytes a second, 0 for none")
	}
	if p.Src == p.Dst {
		return nil, invalidParam("dst", "the copy would land on the file it copies")
	}
	o := transferOptions{
		Move: p.Move, Conflict: p.Conflict, Each: p.Each, Place: p.Place, RateLimit: p.RateLimit,
		NoPerms: p.Perms != nil && !*p.Perms, NoTimes: p.Times != nil && !*p.Times, NoCompress: p.Compress != nil && !*p.Compress,
	}
	if p.Src.Host != p.Dst.Host && (p.KeepSuffix == nil || *p.KeepSuffix) {
		from := p.Src.Host
		if from == "" {
			from = thisMachineLabel()
		}
		o.Label = "from " + from
	}
	if verr := d.transferGrantStart(cs, &p.Src, &p.Dst, &o); verr != nil {
		return nil, verr
	}
	// A destination that is there is the caller's question to put to the
	// person, so it is answered before a job exists.
	if (p.Conflict == "" || p.Conflict == "fail") && p.Place == "" {
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
				Accepted: []string{"replace", "keep-both", "merge", "skip", "ask"},
				Detail:   "Say what to do with the file that is there: replace it, keep both, merge a folder into it, skip what differs, or ask.",
			})
		}
	}
	j, err := d.transfers.start(p.Src, p.Dst, o)
	if err != nil {
		return nil, busyTransferError(err)
	}
	return j.row(), nil
}

func (d *Daemon) verbTransferList(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		ID string `json:"id"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	pane := d.transferGrantPane(cs)
	all := d.transfers.rows()
	rows := all[:0]
	for _, r := range all {
		if pane == "" || r.Pane == pane {
			rows = append(rows, r)
		}
	}
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
		if !transferEnded(r.State) {
			active++
		}
	}
	return map[string]any{"transfers": rows, "active": active}, nil
}

// ErrVerbNoTransfer is a transfer id the daemon does not hold.
const ErrVerbNoTransfer = "no_transfer"

// busyTransferError is the answer to a copy that start refused.
func busyTransferError(err error) *verbError {
	return hintedVerbError(ErrVerbBusy, "another copy writes this path now: "+err.Error(), &VerbHint{
		Verb:   "transfer-list",
		Detail: "Nothing was started. Wait for that copy to end, or cancel it with transfer-cancel, then start this one again.",
	})
}

// transferJobFor is the job id names, if the caller on cs may act on it: any
// job for the person and a pane with admin, and only its own for a pane with
// the files grant.
func (d *Daemon) transferJobFor(cs *connState, id string) (*transferJob, *verbError) {
	j := d.transfers.get(id)
	if j == nil {
		return nil, newVerbError(ErrVerbNoTransfer, "no transfer "+echoName(id))
	}
	if pane := d.transferGrantPane(cs); pane != "" && j.opts.Pane != pane {
		return nil, newVerbError(ErrVerbNoTransfer, "no transfer "+echoName(id))
	}
	return j, nil
}

func transferControl(op string) verbHandler {
	return func(d *Daemon, cs *connState, params json.RawMessage) (any, *verbError) {
		var p struct {
			ID string `json:"id"`
		}
		if verr := decodeParams(params, &p); verr != nil {
			return nil, verr
		}
		j, verr := d.transferJobFor(cs, p.ID)
		if verr != nil {
			return nil, verr
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
				d.transfers.note(j, false)
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
				j.failedFiles = 0
				go d.transfers.run(j)
			}
		}
		j.mu.Unlock()
		d.transfers.note(j, false)
		return j.row(), nil
	}
}

// verbTransferAnswer answers the conflicts a copy in state conflict waits on.
func (d *Daemon) verbTransferAnswer(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		ID     string   `json:"id"`
		Rel    string   `json:"rel"`
		Rels   []string `json:"rels"`
		Choice string   `json:"choice"`
		All    bool     `json:"all"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	switch p.Choice {
	case "replace", "keep-both", "skip":
	default:
		return nil, invalidParam("choice", "choice is replace, keep-both or skip", "replace", "keep-both", "skip")
	}
	if p.Rel != "" {
		p.Rels = append(p.Rels, p.Rel)
	}
	if len(p.Rels) == 0 && !p.All {
		return nil, invalidParam("rel", "name the file with rel or rels, or answer every file with all")
	}
	j, verr := d.transferJobFor(cs, p.ID)
	if verr != nil {
		return nil, verr
	}
	j.mu.Lock()
	if p.All {
		j.answerAll = p.Choice
	}
	for _, rel := range p.Rels {
		j.answers[rel] = p.Choice
	}
	j.mu.Unlock()
	select {
	case j.answered <- struct{}{}:
	default:
	}
	LogBasic("Transfer %s: %s for %d files (all %v)", j.id, p.Choice, len(p.Rels), p.All)
	return j.row(), nil
}

// verbTransferClear drops the copies that ended from the list.
func (d *Daemon) verbTransferClear(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		IDs []string `json:"ids"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	pane := d.transferGrantPane(cs)
	want := map[string]bool{}
	for _, id := range p.IDs {
		want[id] = true
	}
	m := d.transfers
	m.mu.Lock()
	n := m.dropLocked(func(j *transferJob) bool {
		return transferEnded(j.state) && (pane == "" || j.opts.Pane == pane) && (len(want) == 0 || want[j.id])
	})
	m.mu.Unlock()
	return map[string]any{"cleared": n}, nil
}

func transferVerbs() map[string]verbEntry {
	endpoint := func(name, what string) verbParam {
		return verbParam{Name: name, Type: "object", Required: true, Description: what + ": {host, path}. host is a name from [hosts], or empty for this machine. path is absolute or starts with ~."}
	}
	rowReturn := []verbParam{
		{Name: "id", Type: "string", Description: "The transfer's id."},
		{Name: "state", Type: "string", Description: "queued, running, verifying, waiting (for a machine that went away), paused, conflict (waits for transfer-answer), done, failed or cancelled.", Accepted: []string{transferQueued, transferRunning, transferVerifying, transferWaiting, transferPaused, transferConflict, transferDone, transferFailed, transferCancelled}},
		{Name: "size", Type: "int", Description: "Bytes to copy in all."},
		{Name: "done", Type: "int", Description: "Bytes copied, or found to be there already."},
		{Name: "wire", Type: "int", Description: "Bytes that crossed a link, fewer than done when they were compressed."},
		{Name: "rate", Type: "float", Description: "Bytes per second over the last five seconds."},
		{Name: "eta_ms", Type: "int", Description: "Time left, once two seconds of data came."},
		{Name: "resumed_from", Type: "int", Description: "Where the copy went on from after a machine came back. Absent when it never had to."},
		{Name: "verified", Type: "bool", Description: "The copy's sha256 matched the original's."},
		{Name: "final", Type: "string", Description: "Where the file is now, which keep-both can change."},
		{Name: "same", Type: "int", Description: "Files that were there with the same bytes, which were not copied again."},
		{Name: "conflicts_skipped", Type: "int", Description: "Files that were there and differed, left as they were."},
		{Name: "conflicts", Type: "[]object", Description: "In state conflict: the files that wait for an answer, rel, path, src_size, src_mtime, dst_size, dst_mtime."},
		{Name: "error", Type: "string", Description: "What happened and what to do, for failed and waiting."},
	}
	idParam := verbParam{Name: "id", Type: "string", Required: true, Description: "The transfer's id."}
	return map[string]verbEntry{
		"transfer-start": {
			description: "Copy a file or a folder between this machine and a host, or between two hosts. The daemon runs the copy, so a client may quit, and a daemon restart goes on with it. A machine that goes away pauses it, and it goes on from where it stopped when the machine is back. Every file's sha256 is checked before it is put in place, and it keeps the original's permission bits and modification time. A file that is there with the same sha256 is not copied again. A destination that exists answers file_exists unless conflict says what to do. A destination another copy writes now answers busy. Each change is a transfer event on the event stream.",
			params: []verbParam{
				endpoint("src", "What to copy"),
				endpoint("dst", "Where it goes: the full path of the copy, or with place a folder"),
				{Name: "move", Type: "bool", Description: "Remove the original once the copy is checked."},
				{Name: "conflict", Type: "string", Description: "When dst exists: replace it, keep both (the copy gets a new name), merge a folder into the folder there, skip a file that differs, ask (the copy waits in state conflict for transfer-answer), or fail.", Accepted: []string{"replace", "keep-both", "merge", "skip", "ask", "fail"}, Default: "fail"},
				{Name: "each", Type: "string", Description: "In a folder copy into a folder that is there: what to do with each file that is there and differs. Omit to follow conflict, which for merge and replace is replace.", Accepted: []string{"replace", "keep-both", "skip", "ask"}},
				{Name: "place", Type: "string", Description: "auto reads dst as cp does: a folder that is there gets the copy inside it, and anything else is the new name. into needs dst to be a folder that is there. A dst that ends in / with auto is into. contents copies what a folder holds into dst, and a file as auto does.", Accepted: []string{"auto", "into", "contents"}},
				{Name: "perms", Type: "bool", Description: "Give each file the original's permission bits.", Default: "true"},
				{Name: "times", Type: "bool", Description: "Give each file the original's modification time.", Default: "true"},
				{Name: "compress", Type: "bool", Description: "Compress the bytes on a link when they compress well.", Default: "true"},
				{Name: "rate_limit", Type: "int", Description: "At most this many bytes a second. 0 for no limit.", Default: "0"},
				{Name: "keep_from", Type: "bool", Description: "keep-both names a copy from another machine \"name (from HOST).ext\". false numbers it, \"name 2.ext\".", Default: "true"},
			},
			returns:  rowReturn,
			examples: []string{`{"id":1,"verb":"transfer-start","params":{"src":{"host":"build","path":"~/out/image.iso"},"dst":{"path":"~/Downloads/image.iso"}}}`},
			handler:  (*Daemon).verbTransferStart,
		},
		"transfer-list": {
			description: "List the transfers, newest first, with byte progress, rate and time left. Finished ones stay for 30 minutes. A pane with the files grant sees only the copies it started.",
			params:      []verbParam{{Name: "id", Type: "string", Description: "Only this transfer."}},
			returns: []verbParam{
				{Name: "transfers", Type: "[]object", Description: "One row per transfer: id, name, src, dst, target, kind, move, state, size, done, wire, rate, eta_ms, files, files_done, current, current_done, current_size, error, code, resumed_from, resumes, sha256, verified, created, started, ended, retry_in_ms, skipped, skipped_items, same, conflicts_skipped, conflicts_left, conflicts, conflict_count, failed_files, pane, pane_session."},
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
		"transfer-answer": {
			description: "Answer a copy in state conflict: what to do with files that are there and differ. Answer one file, several, or every file with all. The copy goes on when every file it waits on has an answer.",
			params: []verbParam{
				idParam,
				{Name: "choice", Type: "string", Required: true, Description: "replace the file there, keep both (the copy gets a new name), or skip the file.", Accepted: []string{"replace", "keep-both", "skip"}},
				{Name: "rel", Type: "string", Description: "The file, as the row's conflicts name it."},
				{Name: "rels", Type: "[]string", Description: "Several files."},
				{Name: "all", Type: "bool", Description: "This answer for every file of the copy, now and later."},
			},
			returns:  rowReturn,
			examples: []string{`{"id":1,"verb":"transfer-answer","params":{"id":"3f9a1c2b7d00","choice":"keep-both","all":true}}`},
			handler:  (*Daemon).verbTransferAnswer,
		},
		"transfer-clear": {
			description: "Remove the transfers that ended (done, failed, cancelled) from the list. A failed one takes its parts with it.",
			params:      []verbParam{{Name: "ids", Type: "[]string", Description: "Only these transfers. Omit for every one that ended."}},
			returns:     []verbParam{{Name: "cleared", Type: "int", Description: "How many left the list."}},
			examples:    []string{`{"id":1,"verb":"transfer-clear"}`},
			handler:     (*Daemon).verbTransferClear,
		},
	}
}
