package session

import (
	"context"
	"errors"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
	"tuios.dev/vt"
)

// HandoffVersion is the manifest version this build writes, and
// MinHandoffVersion the oldest it reads. A change to what a record means, or
// to a field a record carries, moves HandoffVersion.
//
// Version 1 is two formats: v0.11.1 wrote the daemon record as gob, and later
// builds wrote it as JSON under the same number. Version 2 is the JSON one,
// and no build reads version 1 any more.
//
// Version 3 added the daemon record's keeper. A version 2 manifest reads with
// no keeper, which is what a version 2 image started, so MinHandoffVersion
// stays at 2.
const (
	HandoffVersion    = 3
	MinHandoffVersion = 2
)

// ErrHandoffUnsupported is a platform with no live handoff. Its message is
// built when it is shown, so the restart-server it names carries the --server
// of the daemon or command that returns it.
var ErrHandoffUnsupported error = handoffUnsupported{}

type handoffUnsupported struct{}

func (handoffUnsupported) Error() string {
	return "live upgrade is not supported on this platform. " + restartAdvice()
}

var (
	errHandoffBusy       = errors.New("a live upgrade is already running")
	errHandoffEmbedded   = errors.New("this daemon runs inside another program (tuios-web or the SSH server), and replacing its image would replace that program")
	errHandoffHostedOpen = errors.New("a live upgrade of this daemon is running. Try again once it is done")
)

const handoffQuiesceTimeout = 10 * time.Second

const handoffCommitTimeout = 5 * time.Second

const handoffWorkers = 8

type handoffRequest struct {
	binary         string
	pinned         *fileID
	shownExited    []string
	holdAfterWrite time.Duration
	inWindow       func()
	afterCheck     func()
	copyRoute      bool
}

type handoffRun struct {
	d    *Daemon
	req  handoffRequest
	bin  handoffBinary
	prev *prevBinary
	file *os.File
	fds  []int
	undo []func()

	mu    sync.Mutex
	state int
	timer *time.Timer

	panes         int
	manifestBytes int64
	closedRemote  []string
	started       time.Time
	stats         HandoffStats
}

// HandoffStats is what a live handoff measured. The image that hands off
// fills in its half and carries it in the manifest; the image that adopts
// adds the other half.
type HandoffStats struct {
	// From is the build the panes were handed over by.
	From  string `json:"from"`
	Panes int    `json:"panes"`
	// ManifestBytes is the size of the manifest.
	ManifestBytes int64 `json:"manifest_bytes"`
	// PreflightMS is the run of the new binary before anything paused.
	PreflightMS float64 `json:"preflight_ms"`
	// QuiesceMS is stopping every pane, and CaptureMS writing the manifest.
	QuiesceMS float64 `json:"quiesce_ms"`
	CaptureMS float64 `json:"capture_ms"`
	// PendingMax and PendingTotal are the bytes the kernel held unread in
	// the panes' PTYs once they stopped: what the programs wrote meanwhile,
	// which the new image reads. PendingMax is the most for one pane.
	PendingMax   int `json:"pending_max"`
	PendingTotal int `json:"pending_total"`
	// StoppedAt is when the first pane stopped, in Unix nanoseconds.
	StoppedAt int64 `json:"stopped_at"`

	// ApplyMS is rebuilding every pane's emulator in the new image.
	// ApplyHeapStart, ApplyPeakHeapInuse and ApplyHeapEnd are the heap in
	// use before, at its most during, and after it, with the manifest read.
	ApplyMS            float64 `json:"apply_ms,omitempty"`
	ApplyHeapStart     uint64  `json:"apply_heap_start,omitempty"`
	ApplyPeakHeapInuse uint64  `json:"apply_peak_heap_inuse,omitempty"`
	ApplyHeapEnd       uint64  `json:"apply_heap_end,omitempty"`
	// PausedMS is from the first pane stopping to every pane reading again.
	PausedMS float64 `json:"paused_ms,omitempty"`
	// Fallback says the new image could not adopt the panes, and the
	// previous binary did.
	Fallback bool `json:"fallback,omitempty"`
	// Recovered says the new image died before it was up, and the keeper
	// started the previous binary in its place, with a pid of its own.
	Recovered bool `json:"recovered,omitempty"`
}

type handoffPaneRef struct {
	sess   *Session
	pty    *PTY
	window string
}

func handoffDir() string {
	return filepath.Join(getResurrectionDir(), "handoff")
}

func (d *Daemon) prepareHandoff(req handoffRequest) (*handoffRun, error) {
	if err := d.handoffRefusal(); err != nil {
		return nil, err
	}
	if !d.handoffMu.TryLock() {
		return nil, errHandoffBusy
	}
	run := &handoffRun{d: d, req: req, started: time.Now()}
	run.stats.From = d.version
	prev, err := d.checkPrevBinary()
	if err != nil {
		d.handoffMu.Unlock()
		return nil, err
	}
	run.prev = prev
	t0 := time.Now()
	if run.bin, err = preflightBinary(req.binary, preflightOpts{afterCheck: req.afterCheck, copy: req.copyRoute}); err != nil {
		d.handoffMu.Unlock()
		return nil, err
	}
	run.stats.PreflightMS = msSince(t0)
	if req.pinned != nil && run.bin.file != *req.pinned {
		run.bin.close()
		d.handoffMu.Unlock()
		return nil, errBinaryReplaced(run.bin.path)
	}
	if err := run.prepare(); err != nil {
		run.bin.close()
		run.putBack()
		d.handoffMu.Unlock()
		return nil, err
	}
	run.timer = time.AfterFunc(handoffCommitTimeout, func() {
		if run.abandon() {
			log.Printf("Live upgrade abandoned: it was not committed within %s", handoffCommitTimeout)
		}
	})
	return run, nil
}

func (r *handoffRun) prepare() error {
	d := r.d

	d.pauseAccepting()
	r.undo = append(r.undo, d.resumeAccepting)

	d.verifies.hold(verifyWaitDelay + time.Second)
	r.undo = append(r.undo, func() {
		d.verifies.release()
		for _, sess := range d.manager.AllSessions() {
			d.resumeVerifies(sess)
		}
	})
	d.plugins.stop()
	d.fleet.stop()
	if d.federation != nil {
		d.federation.Stop()
	}
	d.awake.stop()
	if d.hooks != nil {
		d.hooks.WaitTimeout(hookDrainTimeout)
	}
	r.undo = append(r.undo, func() {
		if d.federation != nil {
			d.federation.Resume(d.ctx)
		}
		d.fleet.start(d.ctx)
		d.plugins.restart()
		d.awake.start(d.ctx)
	})

	shut := map[*PTY]bool{}
	shutAll := func() {
		for _, sess := range d.manager.AllSessions() {
			for _, p := range sess.allPTYs() {
				if !shut[p] {
					p.reap.shut()
					shut[p] = true
				}
			}
		}
	}
	shutAll()
	r.undo = append(r.undo, func() {
		for p := range shut {
			p.reap.open()
		}
	})
	d.manager.paneLife.Lock()
	r.undo = append(r.undo, d.manager.paneLife.Unlock)
	shutAll()
	if err := d.handoffRefusal(); err != nil {
		return err
	}
	if what := d.pipes.shut(); what != "" {
		return pipeRefusal(what)
	}
	r.undo = append(r.undo, d.pipes.open)

	if n := d.shutHostedPanes(); n > 0 {
		return hostedRefusal(n)
	}
	r.undo = append(r.undo, d.openHostedPanes)

	d.stopRecordings("daemon upgrade")

	closed, err := closeExitedRemotes(d.manager.AllSessions(), r.req.shownExited)
	if err != nil {
		return err
	}
	r.closedRemote = closed

	sessions := d.manager.AllSessions()
	for _, sess := range sessions {
		sess.holdReportColors()
	}
	r.undo = append(r.undo, func() {
		for _, sess := range sessions {
			sess.releaseReportColors()
		}
	})
	var refs []handoffPaneRef
	for _, sess := range sessions {
		for _, w := range sess.GetState().Windows {
			if w.PTYID == "" {
				continue
			}
			p := sess.GetPTY(w.PTYID)
			if p == nil {
				continue
			}
			if p.host != "" {
				return remotePaneRefusal(sessions)
			}
			refs = append(refs, handoffPaneRef{sess: sess, pty: p, window: w.ID})
		}
	}
	r.panes = len(refs)

	f, err := newHandoffFile(handoffDir())
	if err != nil {
		return errfmt.Errorf("making the handoff manifest: %w", err)
	}
	r.file = f
	r.undo = append(r.undo, func() { _ = f.Close() })
	hold := r.keptDescriptors(refs)
	bin := keeperBinary(r.prev, hold)
	restore := append([]string{bin}, d.handoffArgs(fdNum(f))...)
	k, err := startKeeper(bin, hold, restore)
	if err != nil {
		return errfmt.Errorf("starting the handoff keeper: %w", err)
	}
	d.setKeeper(k)
	r.undo = append(r.undo, d.releaseKeeper)

	ctx, cancel := context.WithTimeout(d.ctx, handoffQuiesceTimeout)
	defer cancel()
	quiesced := make([]*PTY, len(refs))
	r.undo = append(r.undo, func() {
		for _, p := range quiesced {
			if p != nil {
				p.Resume()
			}
		}
	})
	t0 := time.Now()
	r.stats.StoppedAt = t0.UnixNano()
	if err := forEachBounded(len(refs), handoffWorkers, func(i int) error {
		quiesced[i] = refs[i].pty
		if err := refs[i].pty.Quiesce(ctx); err != nil {
			return errfmt.Errorf("stopping pane %s: %w", shortID(refs[i].window), err)
		}
		return nil
	}); err != nil {
		return err
	}
	r.stats.QuiesceMS = msSince(t0)
	for _, ref := range refs {
		ptyspawn.ControlMaster(ref.pty.pty, func(fd int) {
			if n := pendingBytes(fd); n > 0 {
				r.stats.PendingMax = max(r.stats.PendingMax, n)
				r.stats.PendingTotal += n
			}
		})
	}

	if err := r.writeManifest(sessions, refs); err != nil {
		return errfmt.Errorf("writing the handoff manifest: %w", err)
	}
	return nil
}

func msSince(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000
}

func (r *handoffRun) writeManifest(sessions []*Session, refs []handoffPaneRef) error {
	d := r.d
	mw, err := newManifestWriter(r.file)
	if err != nil {
		return err
	}
	if err := mw.json(handoffRecHeader, handoffHeader{
		Version:  HandoffVersion,
		Writer:   d.version,
		Backend:  vt.Backend,
		Sessions: len(sessions),
		Panes:    len(refs),
	}); err != nil {
		return err
	}
	for _, sess := range sessions {
		if err := mw.json(handoffRecSession, sess.handoffRecord()); err != nil {
			return err
		}
	}
	t0 := time.Now()
	recs := make([][]byte, len(refs))
	fds := make([]int, len(refs))
	for start := 0; start < len(refs); start += handoffWorkers {
		end := min(start+handoffWorkers, len(refs))
		if err := forEachBounded(end-start, handoffWorkers, func(i int) error {
			ref := refs[start+i]
			rec, ok := ref.pty.handoffRecord(ref.sess, ref.window)
			if !ok {
				return errfmt.Errorf("pane %s has no terminal device here", shortID(ref.window))
			}
			rec.State, rec.Live = ref.pty.handoffCapture()
			var err error
			recs[start+i], err = encodeGob(rec)
			fds[start+i] = rec.FD
			return err
		}); err != nil {
			return err
		}
		for i := start; i < end; i++ {
			if err := mw.record(handoffRecPane, recs[i]); err != nil {
				return err
			}
			recs[i] = nil
			r.fds = append(r.fds, fds[i])
		}
	}
	r.stats.CaptureMS = msSince(t0)
	r.stats.Panes = len(refs)

	dr := handoffDaemon{
		SocketPath:   d.manager.SocketPath(),
		Listeners:    map[string]int{},
		LockFD:       -1,
		PaneTokenKey: d.manager.paneTokenKey,
		Events:       d.events.handoffState(),
		Mail:         d.agents.handoffState(),
		Fallback:     r.prev.path,
		FallbackFD:   r.prev.fd(),
		Stats:        r.stats,
		FarHolds:     d.remoteHolds.far(),
	}
	if dr.FallbackFD >= 0 {
		r.fds = append(r.fds, dr.FallbackFD)
	}
	for name, l := range d.namedListeners() {
		var fd int
		if !listenerFD(l, func(n int) { fd = n }) {
			return errfmt.Errorf("the %s socket has no descriptor to hand over", name)
		}
		dr.Listeners[name] = fd
		r.fds = append(r.fds, fd)
	}
	if d.startLock != nil {
		dr.LockFD = fdNum(d.startLock)
		r.fds = append(r.fds, dr.LockFD)
	}
	if dr.Keeper = d.keeperRecord(); dr.Keeper.PID > 0 {
		r.fds = append(r.fds, dr.Keeper.FD)
	}
	if err := mw.json(handoffRecDaemon, dr); err != nil {
		return err
	}
	n, err := mw.finish()
	if err != nil {
		return err
	}
	r.manifestBytes = n
	if err := r.file.Sync(); err != nil {
		return err
	}
	if _, err := r.file.Seek(0, 0); err != nil {
		return err
	}
	r.fds = append(r.fds, fdNum(r.file))
	return nil
}

func (d *Daemon) shutHostedPanes() int {
	d.hostedPanesMu.Lock()
	defer d.hostedPanesMu.Unlock()
	if n := len(d.hostedPanes); n > 0 {
		return n
	}
	d.hostedPanesShut = true
	return 0
}

func (d *Daemon) openHostedPanes() {
	d.hostedPanesMu.Lock()
	d.hostedPanesShut = false
	d.hostedPanesMu.Unlock()
}

func (r *handoffRun) keptDescriptors(refs []handoffPaneRef) []int {
	d := r.d
	out := []int{fdNum(r.file)}
	for _, ref := range refs {
		ptyspawn.ControlMaster(ref.pty.pty, func(fd int) { out = append(out, fd) })
	}
	for _, l := range d.namedListeners() {
		listenerFD(l, func(fd int) { out = append(out, fd) })
	}
	if d.startLock != nil {
		out = append(out, fdNum(d.startLock))
	}
	if fd := r.prev.fd(); fd >= 0 {
		out = append(out, fd)
	}
	return out
}

func (d *Daemon) namedListeners() map[string]net.Listener {
	out := map[string]net.Listener{}
	for name, l := range map[string]net.Listener{
		"main": d.listener, "link": d.linkListener, "link-human": d.linkHumanListener, "herdr": d.herdrListener,
	} {
		if l != nil {
			out[name] = l
		}
	}
	return out
}

func (r *handoffRun) commit() error {
	r.mu.Lock()
	if r.state != 0 {
		r.mu.Unlock()
		return errors.New("the live upgrade was abandoned before it could be committed")
	}
	r.state = 1
	r.mu.Unlock()
	r.timer.Stop()
	d := r.d

	if r.req.holdAfterWrite > 0 {
		time.Sleep(r.req.holdAfterWrite)
	}
	d.attention.saveNow()
	if !r.bin.stillThere() {
		if r.req.pinned != nil {
			r.bin.close()
			r.putBack()
			d.handoffMu.Unlock()
			return errBinaryReplaced(r.bin.path)
		}
		bin, err := preflightBinary(r.bin.path, preflightOpts{copy: r.req.copyRoute})
		r.bin.close()
		if err != nil {
			r.putBack()
			d.handoffMu.Unlock()
			return err
		}
		r.bin = bin
	}
	d.closeClientsForHandoff(r.bin.accepts.Version, r.bin.path)

	argv := append([]string{r.bin.path}, d.handoffArgs(fdNum(r.file))...)
	log.Printf("Live upgrade: exec %s with %d panes (manifest %d bytes, stopped %s)", r.bin.path, r.panes, r.manifestBytes, time.Since(r.started).Round(time.Millisecond))
	path := r.bin.execTarget()
	worktree.RemoveTemp(tempRemoveWait)
	err := execHandoff(path, argv, withOldCopies(withoutEnv(os.Environ(), handoffFallbackEnv)), r.fds, r.req.inWindow)
	LogError("Live upgrade: exec %s failed, the daemon carries on: %v", r.bin.path, err)
	r.bin.close()
	r.putBack()
	d.handoffMu.Unlock()
	return errfmt.Errorf("exec %s: %w", r.bin.path, err)
}

func (r *handoffRun) abandon() bool {
	r.mu.Lock()
	if r.state != 0 {
		r.mu.Unlock()
		return false
	}
	r.state = 2
	r.mu.Unlock()
	r.bin.close()
	r.putBack()
	r.d.handoffMu.Unlock()
	return true
}

func (r *handoffRun) putBack() {
	for i := len(r.undo) - 1; i >= 0; i-- {
		r.undo[i]()
	}
	r.undo = nil
}

func (s *Session) handoffRecord() handoffSession {
	st := s.GetState()
	rec := handoffSession{
		State:           st,
		ID:              s.ID,
		StartDir:        s.StartDir(),
		WindowStartDirs: make(map[string]string, len(st.Windows)),
	}
	for _, w := range st.Windows {
		if dir := s.windowStartDir(w.ID); dir != "" {
			rec.WindowStartDirs[w.ID] = dir
		}
	}
	if used := s.LastUsed(); !used.IsZero() {
		rec.LastUsed = used.UnixNano()
	}
	return rec
}

func (p *PTY) handoffRecord(sess *Session, window string) (handoffPane, bool) {
	rec := handoffPane{ID: p.ID, Session: sess.ID, Window: window, FD: -1, TTY: p.TTYName(), KeepOpen: p.keepOpen != nil}
	if !ptyspawn.ControlMaster(p.pty, func(fd int) { rec.FD = fd }) || rec.FD < 0 {
		return rec, false
	}
	if p.proc != nil {
		rec.PID = p.proc.Pid
	}
	p.terminalMu.RLock()
	rec.Width, rec.Height = p.width, p.height
	p.terminalMu.RUnlock()
	rec.SpawnW, rec.SpawnH = p.spawnW, p.spawnH
	if code, exited := p.ExitStatus(); exited {
		rec.Exited, rec.ExitCode, rec.ExitSignal = true, code, p.ExitSignal()
	}
	return rec, true
}

func (s *Session) allPTYs() []*PTY {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	out := make([]*PTY, 0, len(s.ptys))
	for _, p := range s.ptys {
		out = append(out, p)
	}
	return out
}

func forEachBounded(n, workers int, fn func(i int) error) error {
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
		next  = make(chan int)
	)
	for range min(workers, n) {
		wg.Go(func() {
			for i := range next {
				if err := fn(i); err != nil {
					mu.Lock()
					if first == nil {
						first = err
					}
					mu.Unlock()
				}
			}
		})
	}
	for i := range n {
		next <- i
	}
	close(next)
	wg.Wait()
	return first
}
