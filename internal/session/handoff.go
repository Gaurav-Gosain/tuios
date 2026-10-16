package session

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"tuios.dev/vt"
)

// Live handoff: the daemon execs a new image of itself, and every pane's
// process keeps running under it with the same pid, the same terminal and the
// same screen. See docs/design/live-handoff.md.
//
// The daemon's pid does not change, so it stays the parent of every pane and
// the listening sockets are never closed: a caller that dials during the swap
// waits in the kernel's backlog and is answered by the new image. What crosses
// the exec is descriptors (every pane's PTY master, the sockets, the start
// lock) and a manifest on one more descriptor saying what they are.
//
// The order matters. Everything that can fail (stopping the readers, writing
// the manifest) comes before the clients are told, and a failure there puts
// every part back as it was. After the clients are closed only the exec is
// left, and an exec that fails puts the daemon back too, minus its clients.

// HandoffVersion is the manifest version this build writes, and
// MinHandoffVersion the oldest it reads. A change to what a record means, or
// to a field a record carries, moves HandoffVersion.
const (
	HandoffVersion    = 1
	MinHandoffVersion = 1
)

// ErrHandoffUnsupported is a platform with no live handoff.
var ErrHandoffUnsupported = errors.New("live upgrade is not supported on this platform. 'tuios kill-server' restarts the daemon, which ends running programs")

var (
	errHandoffBusy     = errors.New("a live upgrade is already running")
	errHandoffEmbedded = errors.New("this daemon runs inside another program (tuios-web or the SSH server), and replacing its image would replace that program")
)

// handoffQuiesceTimeout bounds how long the panes may take to stop.
const handoffQuiesceTimeout = 10 * time.Second

// handoffCommitTimeout is how long a prepared handoff waits for the reply
// that announced it to be written before it gives up and puts everything back.
const handoffCommitTimeout = 5 * time.Second

// handoffWorkers bounds how many panes are stopped and written at once.
const handoffWorkers = 8

// handoffRequest is one handoff.
type handoffRequest struct {
	// binary is the image to exec.
	binary string
	// holdAfterWrite and inWindow are for tests: a wait after the manifest
	// is written, and a function run while the descriptors are exposed.
	holdAfterWrite time.Duration
	inWindow       func()
}

// handoffRun is a handoff that has stopped the daemon's parts, holding what it
// needs to put each one back.
type handoffRun struct {
	d    *Daemon
	req  handoffRequest
	file *os.File
	fds  []int
	// undo puts back what has been stopped, last first.
	undo []func()

	mu    sync.Mutex
	state int // 0 prepared, 1 committing, 2 abandoned
	timer *time.Timer

	// What the run measured, for the caller.
	panes         int
	manifestBytes int64
	started       time.Time
}

// handoffPaneRef is a pane the manifest carries.
type handoffPaneRef struct {
	sess   *Session
	pty    *PTY
	window string
}

// handoffDir is where the manifest is made on a platform without memfd.
func handoffDir() string {
	return filepath.Join(getResurrectionDir(), "handoff")
}

// prepareHandoff stops the daemon at a point where nothing of a pane is in
// its hands, and writes the manifest. On error nothing has changed. On success
// the daemon is stopped until commit or abandon.
func (d *Daemon) prepareHandoff(req handoffRequest) (*handoffRun, error) {
	if !handoffSupported() {
		return nil, ErrHandoffUnsupported
	}
	if d.handoffArgs == nil {
		return nil, errHandoffEmbedded
	}
	if !d.handoffMu.TryLock() {
		return nil, errHandoffBusy
	}
	run := &handoffRun{d: d, req: req, started: time.Now()}
	if err := run.prepare(); err != nil {
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

	// New callers wait in the backlog from here on.
	d.pauseAccepting()
	r.undo = append(r.undo, d.resumeAccepting)

	// The reaps first: a reap in progress runs the pane's exit, which may
	// close the pane, and that needs paneLife.
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
	// A pane started between the two is in now, and its gate shuts too.
	shutAll()

	sessions := d.manager.AllSessions()
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
				return fmt.Errorf("window %s runs on %s, and a pane on another machine cannot be handed over. Close it first", shortID(w.ID), p.host)
			}
			refs = append(refs, handoffPaneRef{sess: sess, pty: p, window: w.ID})
		}
	}
	r.panes = len(refs)

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
	if err := forEachBounded(len(refs), handoffWorkers, func(i int) error {
		quiesced[i] = refs[i].pty
		if err := refs[i].pty.Quiesce(ctx); err != nil {
			return fmt.Errorf("stopping pane %s: %w", shortID(refs[i].window), err)
		}
		return nil
	}); err != nil {
		return err
	}

	f, err := newHandoffFile(handoffDir())
	if err != nil {
		return fmt.Errorf("making the handoff manifest: %w", err)
	}
	r.file = f
	r.undo = append(r.undo, func() { _ = f.Close() })
	if err := r.writeManifest(sessions, refs); err != nil {
		return fmt.Errorf("writing the handoff manifest: %w", err)
	}
	return nil
}

// writeManifest writes every record and collects the descriptors the exec
// keeps.
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
	for _, ref := range refs {
		rec, ok := ref.pty.handoffRecord(ref.sess, ref.window)
		if !ok {
			return fmt.Errorf("pane %s has no terminal device here", shortID(ref.window))
		}
		if err := mw.gob(handoffRecPane, rec); err != nil {
			return err
		}
		r.fds = append(r.fds, rec.FD)
	}

	dr := handoffDaemon{SocketPath: d.manager.SocketPath(), Listeners: map[string]int{}, LockFD: -1}
	for name, l := range d.namedListeners() {
		var fd int
		if !listenerFD(l, func(n int) { fd = n }) {
			return fmt.Errorf("the %s socket has no descriptor to hand over", name)
		}
		dr.Listeners[name] = fd
		r.fds = append(r.fds, fd)
	}
	if d.startLock != nil {
		dr.LockFD = int(d.startLock.Fd()) //nolint:gosec // a descriptor number
		r.fds = append(r.fds, dr.LockFD)
	}
	if err := mw.gob(handoffRecDaemon, dr); err != nil {
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
	r.fds = append(r.fds, int(r.file.Fd())) //nolint:gosec // a descriptor number
	return nil
}

// namedListeners is every listening socket, by the name the manifest gives it.
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

// commit closes every client and execs the new image. It returns only if
// the exec failed, after putting the daemon back.
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
	d.closeClientsForHandoff()

	argv := append([]string{r.req.binary}, d.handoffArgs(int(r.file.Fd()))...) //nolint:gosec // a descriptor number
	log.Printf("Live upgrade: exec %s with %d panes (manifest %d bytes, stopped %s)", r.req.binary, r.panes, r.manifestBytes, time.Since(r.started).Round(time.Millisecond))
	err := execHandoff(r.req.binary, argv, os.Environ(), r.fds, r.req.inWindow)
	LogError("Live upgrade: exec %s failed, the daemon carries on: %v", r.req.binary, err)
	r.putBack()
	d.handoffMu.Unlock()
	return fmt.Errorf("exec %s: %w", r.req.binary, err)
}

// abandon puts back a prepared run that was never committed. It reports
// whether it did.
func (r *handoffRun) abandon() bool {
	r.mu.Lock()
	if r.state != 0 {
		r.mu.Unlock()
		return false
	}
	r.state = 2
	r.mu.Unlock()
	r.putBack()
	r.d.handoffMu.Unlock()
	return true
}

// putBack undoes every step taken, last first.
func (r *handoffRun) putBack() {
	for i := len(r.undo) - 1; i >= 0; i-- {
		r.undo[i]()
	}
	r.undo = nil
}

// closeClientsForHandoff drops every client connection, as shutdown does.
func (d *Daemon) closeClientsForHandoff() {
	d.clientsMu.Lock()
	for _, cs := range d.clients {
		cs.closeDone()
		_ = cs.conn.Close()
	}
	d.clients = make(map[string]*connState)
	d.clientsMu.Unlock()
}

// handoffRecord is the session as the next image rebuilds it.
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

// handoffRecord is the pane as the next image adopts it, and false for a pane
// whose bytes do not come from a descriptor here. The pane is quiesced.
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

// allPTYs is every pane the session holds.
func (s *Session) allPTYs() []*PTY {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	out := make([]*PTY, 0, len(s.ptys))
	for _, p := range s.ptys {
		out = append(out, p)
	}
	return out
}

// forEachBounded runs fn for 0..n-1 on at most workers goroutines and returns
// the first error.
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
