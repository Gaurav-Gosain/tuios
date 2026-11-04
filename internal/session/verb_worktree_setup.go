package session

import (
	"log"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// Worktree setup: a shell line ([worktree] setup, or the call's setup) that
// runs in every new worktree before its agent or shell, such as "pnpm i".
//
// It runs in a window of the new session named setup, through the same pane
// machinery verify-fan uses: sh -c in the worktree, no grants, its status on
// fd 3. The window is the output a person reads, and its screen is where the
// last lines on the record come from, so nothing here keeps a log.
//
// The record moves through stages: the checkout (done before the setup
// starts), the setup (pending, running, passed or failed, with the exit
// status and the last four lines), and the agent (waiting, started or
// not_started). list-worktrees reports it, and the client draws it as the
// setup card.
//
// A sync setup holds the agent until it passes. One that fails before the
// agent starts takes the worktree and the branch away again, but only what
// this call made and nobody touched: a branch the call created that still
// points where it started, and a worktree with no uncommitted change and no
// nested repository. Anything else is kept and the note says why. The session
// is kept either way, with the failed setup's window open, so the output can
// still be read.
//
// An async setup releases the agent at once. A failure after that keeps
// everything, because the agent may already have work in the worktree, and
// only reports.
//
// Nothing here runs at idle. The watcher waits on the status pipe, and the
// last lines follow the pane's output events, read at most once per
// worktreeSetupTailEvery while output arrives and never once it stops.

// Setup states, as the wire carries them.
const (
	SetupPending = "pending"
	SetupRunning = "running"
	SetupPassed  = "passed"
	SetupFailed  = "failed"
)

// The agent stage of a setup, as the wire carries it.
const (
	// SetupAgentWaiting is an agent held until a sync setup passes.
	SetupAgentWaiting = "waiting"
	// SetupAgentStarted is an agent whose window is open: at once for an
	// async setup, after the pass for a sync one.
	SetupAgentStarted = "started"
	// SetupAgentNotStarted is an agent a sync setup's failure kept from
	// starting.
	SetupAgentNotStarted = "not_started"
)

// worktreeSetupWindow is the name of the window a setup runs in.
const worktreeSetupWindow = "setup"

// worktreeSetupTailLines is how many of the setup's last lines the record
// keeps, and worktreeSetupTailWidth how many bytes of each.
const (
	worktreeSetupTailLines = 4
	worktreeSetupTailWidth = 200
)

// worktreeSetupTailEvery is how often, at most, the last lines are read from
// the screen while the setup writes. A package manager writes hundreds of
// lines a second, and each read is a state push to every client.
const worktreeSetupTailEvery = 250 * time.Millisecond

// WorktreeSetup is the setup record of one worktree session.
type WorktreeSetup struct {
	// Command is the shell line, as the config or the call gave it.
	Command string `json:"command"`
	// Async says the agent started at once, beside the setup.
	Async bool `json:"async,omitempty"`
	// State is pending, running, passed or failed.
	State string `json:"state"`
	// Exit is the setup's exit status once it ended, nil while it runs or
	// when the process reported none.
	Exit *int `json:"exit,omitempty"`
	// StartedAt and FinishedAt are Unix nanoseconds. FinishedAt is zero
	// while the setup runs.
	StartedAt  int64 `json:"started_at,omitempty"`
	FinishedAt int64 `json:"finished_at,omitempty"`
	// Tail is the last lines of the setup's screen, at most four.
	Tail []string `json:"tail,omitempty"`
	// Agent is the agent stage: waiting, started or not_started.
	Agent string `json:"agent,omitempty"`
	// Shell says what starts after the setup is a shell, not an agent.
	Shell bool `json:"shell,omitempty"`
	// Window is the setup's window.
	Window string `json:"window,omitempty"`
	// Removed and BranchRemoved say a failed sync setup took the worktree
	// and the branch away again.
	Removed       bool `json:"removed,omitempty"`
	BranchRemoved bool `json:"branch_removed,omitempty"`
	// Note says what the failure did and did not remove, or why a failed
	// setup has no exit status.
	Note string `json:"note,omitempty"`
}

// setupSpec is the setup a new worktree runs: the command, empty for none,
// and whether the agent waits for it.
type setupSpec struct {
	Command string
	Async   bool
}

// setupParam is the part of new-worktree and fan that names a setup. Absent
// takes [worktree] setup. An empty setup runs none.
type setupParam struct {
	Setup      *string `json:"setup"`
	SetupAsync *bool   `json:"setup_async"`
}

// worktreeSetups holds the setup config in force and the setups running now,
// by session ID. The zero value is ready. Its lock is taken inside a
// session's state lock and never the other way round.
type worktreeSetups struct {
	cfg atomic.Pointer[setupSpec]
	// waiting says config.toml names a new setup command that waits for
	// tuios config apply or a restart.
	waiting atomic.Bool

	mu   sync.Mutex
	runs map[string]*setupRun
}

// setupRun is one running setup: its window, the channel that stops its
// watcher, and the pipe end whose close lets the script go on.
type setupRun struct {
	window string
	stop   chan struct{}
	once   sync.Once
	hold   *os.File
	holdMu sync.Mutex
}

func (r *setupRun) halt() { r.once.Do(func() { close(r.stop) }) }

// letGo closes the pipe the script waits on after it reports, so it ends,
// or keeps its window open on a failure.
func (r *setupRun) letGo() {
	r.holdMu.Lock()
	defer r.holdMu.Unlock()
	if r.hold != nil {
		_ = r.hold.Close()
		r.hold = nil
	}
}

// validSetupCommand reports whether cmd is a setup the daemon runs.
func validSetupCommand(cmd string) bool {
	return len(cmd) <= config.WorktreeSetupMaxBytes && !strings.ContainsRune(cmd, 0)
}

// setConfig sets the setup every new worktree runs. A command the
// validation refuses runs nothing, as the config warning says.
func (t *worktreeSetups) setConfig(cmd string, async bool) {
	cmd = strings.TrimSpace(cmd)
	if !validSetupCommand(cmd) {
		cmd = ""
	}
	t.cfg.Store(&setupSpec{Command: cmd, Async: async})
}

// config is the setup in force.
func (t *worktreeSetups) config() setupSpec {
	if c := t.cfg.Load(); c != nil {
		return *c
	}
	return setupSpec{}
}

// applyConfig applies a setup from config.toml and reports whether it did. A
// change by the person applies. From a file change, which a process in a
// pane can make, only a cleared or unchanged command applies: a new command
// is a command the daemon would run, so it waits for the person.
func (t *worktreeSetups) applyConfig(cmd string, async, byPerson bool) bool {
	cmd = strings.TrimSpace(cmd)
	if byPerson || cmd == "" || cmd == t.config().Command {
		t.setConfig(cmd, async)
		return true
	}
	log.Printf("[worktree] The new setup command in config.toml waits for tuios config apply from outside tuios, or a daemon restart. The setup in force stays.")
	return false
}

// resolveSetup is the setup a call runs: its own, or the config's.
func (d *Daemon) resolveSetup(p setupParam) (setupSpec, *verbError) {
	spec := d.setups.config()
	if p.Setup != nil {
		spec.Command = strings.TrimSpace(*p.Setup)
		if len(spec.Command) > config.WorktreeSetupMaxBytes {
			return setupSpec{}, invalidParam("setup", "setup is at most "+strconv.Itoa(config.WorktreeSetupMaxBytes)+" bytes: a shell line, not a script")
		}
		if strings.ContainsRune(spec.Command, 0) {
			return setupSpec{}, invalidParam("setup", "setup holds a NUL byte")
		}
	}
	if p.SetupAsync != nil {
		spec.Async = *p.SetupAsync
	}
	return spec, nil
}

// put records run as the session's setup.
func (t *worktreeSetups) put(session string, run *setupRun) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.runs == nil {
		t.runs = make(map[string]*setupRun)
	}
	t.runs[session] = run
}

// finish removes run when it is still the session's setup, and reports
// whether it was.
func (t *worktreeSetups) finish(session string, run *setupRun) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.runs[session] != run {
		return false
	}
	delete(t.runs, session)
	return true
}

// running reports whether a setup runs in the session now.
func (t *worktreeSetups) running(session string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.runs[session] != nil
}

// halt stops the watcher of the session's setup, when one runs, so a
// removal that is under way is not raced by the setup's own undo.
func (t *worktreeSetups) halt(session string) {
	t.mu.Lock()
	run := t.runs[session]
	delete(t.runs, session)
	t.mu.Unlock()
	if run != nil {
		run.halt()
		run.letGo()
	}
}

// worktreeSetupScript runs the setup and reports its status on fd 3, which
// the setup does not inherit. It then waits for the daemon to close fd 4: a
// sync setup that passed keeps its window until the agent's is open, so the
// session never runs out of windows between the two. A setup that passed
// ends the window. One that failed keeps it open, so the output can be read,
// until the person presses enter.
const worktreeSetupScript = `printf 'tuios setup: %s\n\n' "$1"
sh -c "$1" 3>&- 4<&-
s=$?
printf '%s\n' "$s" >&3
exec 3>&-
read -r _ <&4
exec 4<&-
if [ "$s" -eq 0 ]; then exit 0; fi
printf '\nThe setup exited %s. This window stays open so you can read the output. Press enter to close it.\n' "$s"
read -r _
exit "$s"`

// setupLaunch is what a setup needs to start the agent after it: the agent
// window's options and what to do once it is open.
type setupLaunch struct {
	sess    *Session
	info    *WorktreeInfo
	created bool
	spec    setupSpec
	env     []string
	agent   func() (WindowState, error)
	onAgent func(*Session, WindowState)
}

// startWorktreeSetup opens the setup window in the new session and watches
// it. The record says running once the window is in.
func (d *Daemon) startWorktreeSetup(l setupLaunch) (WindowState, error) {
	var (
		argv    []string
		status  *os.File
		extras  []*os.File
		toClose []*os.File
		hold    *os.File
		release = func() {}
	)
	if runtime.GOOS == "windows" {
		// No descriptor passes to a console process, so the window's own exit
		// status is the setup's, and the window closes either way.
		argv = []string{"cmd.exe", "/c", l.spec.Command}
	} else {
		r3, w3, err := os.Pipe()
		if err != nil {
			return WindowState{}, err
		}
		r4, w4, err := os.Pipe()
		if err != nil {
			_ = r3.Close()
			_ = w3.Close()
			return WindowState{}, err
		}
		status, hold = r3, w4
		extras = []*os.File{w3, r4}
		toClose = extras
		argv = []string{"/bin/sh", "-c", worktreeSetupScript, "tuios-setup", l.spec.Command}
		// Held before the window starts, as verify-fan holds it: a daemon
		// handoff must not carry the window and close the pipe the setup
		// reports on.
		release = d.pipes.hold("a worktree setup is running in session " + l.sess.Name())
	}
	none := Grants(0)
	sessionID := l.sess.ID
	onExit := func(ptyID string) { d.notifyPTYClosed(sessionID, ptyID) }
	win, err := l.sess.AddDaemonWindowWith(NewWindowOptions{
		Name:       worktreeSetupWindow,
		Cwd:        l.info.Path,
		Command:    argv,
		Env:        l.env,
		Grants:     &none,
		Focus:      !l.spec.Async,
		extraFiles: extras,
	}, onExit)
	// The child holds its own copies. Closing these is what lets each end
	// see the other go.
	for _, f := range toClose {
		_ = f.Close()
	}
	if err != nil {
		release()
		if status != nil {
			_ = status.Close()
		}
		if hold != nil {
			_ = hold.Close()
		}
		return WindowState{}, err
	}

	run := &setupRun{window: win.ID, stop: make(chan struct{}), hold: hold}
	d.setups.put(l.sess.ID, run)
	rec := WorktreeSetup{
		Command:   l.spec.Command,
		Async:     l.spec.Async,
		State:     SetupRunning,
		StartedAt: time.Now().UnixNano(),
		Agent:     SetupAgentWaiting,
		Shell:     l.info.Setup != nil && l.info.Setup.Shell,
		Window:    win.ID,
	}
	l.sess.setWorktreeSetup(&rec)

	codes := make(chan *int, 1)
	if status != nil {
		go func() {
			codes <- readVerifyStatus(status, l.sess, win.PTYID)
		}()
	} else {
		go func() {
			code, exited := d.waitPopupExit(l.sess, win, 0, nil)
			if !exited {
				codes <- nil
				return
			}
			codes <- &code
		}()
	}
	go d.followSetupTail(l.sess, run, win.PTYID)
	go d.watchWorktreeSetup(l, run, codes, release)
	return win, nil
}

// watchWorktreeSetup records how a setup ended and does what follows: the
// agent after a sync pass, the undo after a sync failure.
//
// release lets a daemon handoff go ahead again. It runs once the agent of a
// sync pass is open, so a handoff cannot fall between the pass and the agent.
func (d *Daemon) watchWorktreeSetup(l setupLaunch, run *setupRun, codes <-chan *int, release func()) {
	defer release()
	defer run.letGo()
	// The tail follower ends with the run.
	defer run.halt()
	var code *int
	select {
	case code = <-codes:
	case <-run.stop:
		return
	case <-d.ctx.Done():
		return
	}
	sess := l.sess
	// A setup whose session was killed under it (remove-worktree, kill)
	// has nobody left to report to.
	if d.manager.GetSessionByID(sess.ID) != sess {
		d.setups.finish(sess.ID, run)
		return
	}
	passed := code != nil && *code == 0
	done := sess.worktreeSetupOrEmpty()
	done.Exit = code
	done.FinishedAt = time.Now().UnixNano()
	settleSetupOutput(sess, done.Window)
	done.Tail = setupTail(sess, done.Window, done.Tail)
	done.State = SetupFailed
	if passed {
		done.State = SetupPassed
	}
	if code == nil {
		done.Note = "The setup ended with no exit status."
	}

	switch {
	case passed && !l.spec.Async:
		// The agent starts now. Its window opens before the setup's ends,
		// which the script holds open until letGo.
		win, err := l.agent()
		if err != nil {
			done.Agent = SetupAgentNotStarted
			done.Note = "The setup passed, but the agent could not start: " + err.Error()
			sess.setPromptIfWaiting(PromptNotSent, "The agent's window could not start after the setup.")
		} else {
			done.Agent = SetupAgentStarted
			if l.onAgent != nil {
				l.onAgent(sess, win)
			}
		}
	case !passed && !l.spec.Async:
		done.Agent = SetupAgentNotStarted
		removed, branchRemoved, note := d.undoFailedSetup(l)
		done.Removed, done.BranchRemoved = removed, branchRemoved
		done.Note = strings.TrimSpace(done.Note + " " + note)
		sess.setPromptIfWaiting(PromptNotSent, "The setup failed, so the agent did not start.")
	}

	current := false
	_ = sess.mutateState(func(st *SessionState) error {
		current = d.setups.finish(sess.ID, run)
		if current && st.Worktree != nil {
			st.Worktree.Setup = &done
		}
		return nil
	})
	run.letGo()
	if current && passed {
		// The window has ended on its own after letGo. Closing it here takes
		// it out of a session no client is attached to.
		_, _ = sess.CloseDaemonWindow(run.window)
	}
}

// undoFailedSetup takes away what a sync setup's worktree call made, when
// nothing in it is anyone's work: the worktree when it holds no uncommitted
// change and no nested repository, and the branch when the call created it
// and it still points where it started. It reports what it removed and a
// sentence that says so.
func (d *Daemon) undoFailedSetup(l setupLaunch) (removed, branchRemoved bool, note string) {
	info := l.info
	if _, err := os.Stat(info.Path); err != nil {
		return false, false, "The worktree folder was already gone."
	}
	changes, err := worktree.Changes(info.Path)
	switch {
	case err != nil:
		return false, false, "git could not read the worktree's status, so the worktree and the branch are kept."
	case changes > 0:
		return false, false, "The worktree holds " + strconv.Itoa(changes) + " uncommitted " + plural.Word(changes, "change", "changes") + ", so the worktree and the branch are kept."
	}
	if nested, err := worktree.NestedRepo(info.Path); err != nil || nested != "" {
		return false, false, "The worktree holds a nested repository or could not be read, so the worktree and the branch are kept."
	}
	head, err := worktree.HeadCommit(info.Path)
	if err != nil || info.StartCommit == "" || head != info.StartCommit {
		return false, false, "The branch has commits of its own, so the worktree and the branch are kept."
	}
	if err := worktree.Remove(info.RepoRoot, info.Path, false); err != nil {
		return false, false, "git did not remove the worktree: " + strings.TrimSpace(err.Error())
	}
	if !l.created {
		return true, false, "The worktree was removed. The branch " + info.Branch + " was there before, so it is kept."
	}
	if err := worktree.DeleteBranchAt(info.RepoRoot, info.Branch, info.StartCommit); err != nil {
		return true, false, "The worktree was removed. The branch " + info.Branch + " is kept: " + strings.TrimSpace(err.Error())
	}
	return true, true, "The worktree and the branch " + info.Branch + " were removed."
}

// followSetupTail keeps the record's last lines current while the setup
// runs. It wakes on the pane's output events only, and reads the screen at
// most once per worktreeSetupTailEvery: the timer exists only between an
// event and the read it schedules.
func (d *Daemon) followSetupTail(sess *Session, run *setupRun, ptyID string) {
	sub := d.events.subscribe(eventFilter{
		session: sess.Name(),
		sess:    sess,
		ptyID:   ptyID,
		types:   map[string]bool{EventOutput: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)
	var (
		timer *time.Timer
		due   <-chan time.Time
	)
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	window := run.window
	for {
		select {
		case <-run.stop:
			return
		case <-d.ctx.Done():
			return
		case _, ok := <-sub.ch:
			if !ok {
				return
			}
			if due == nil {
				if timer == nil {
					timer = time.NewTimer(worktreeSetupTailEvery)
				} else {
					timer.Reset(worktreeSetupTailEvery)
				}
				due = timer.C
			}
		case <-due:
			due = nil
			if !d.setups.running(sess.ID) {
				return
			}
			// Read outside the state lock: setupTail reads the state.
			tail := setupTail(sess, window, nil)
			if tail == nil {
				continue
			}
			// The same lines cost no state push.
			if wt := sess.Worktree(); wt == nil || wt.Setup == nil || slices.Equal(tail, wt.Setup.Tail) {
				continue
			}
			_ = sess.mutateState(func(st *SessionState) error {
				wt := st.Worktree
				if wt == nil || wt.Setup == nil || wt.Setup.State != SetupRunning || wt.Setup.Window != window {
					return nil
				}
				if slices.Equal(tail, wt.Setup.Tail) {
					return nil
				}
				cp := *wt.Setup
				cp.Tail = tail
				wt.Setup = &cp
				return nil
			})
		}
	}
}

// settleSetupOutput gives the setup's last output time to reach the screen
// before the final tail is read. The status arrives on its own pipe, and the
// lines printed just before it may still be in the pty or on their way to
// the emulator. It waits for the pane to be quiet for a moment, at most a
// third of a second, then for the emulator to apply what it was given. It
// runs once per setup, when it ends.
func settleSetupOutput(sess *Session, window string) {
	var ptyID string
	for _, w := range sess.GetState().Windows {
		if w.ID == window {
			ptyID = w.PTYID
			break
		}
	}
	p := sess.GetPTY(ptyID)
	if p == nil {
		return
	}
	last := p.LastOutput()
	for range 12 {
		time.Sleep(25 * time.Millisecond)
		now := p.LastOutput()
		if now == last && time.Since(time.Unix(0, now)) >= 50*time.Millisecond {
			break
		}
		last = now
	}
	p.streamMu.Lock()
	defer p.streamMu.Unlock()
	if p.vtClosed {
		return
	}
	ack := make(chan struct{})
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	select {
	case p.vtWriteChan <- vtChunk{ack: ack}:
	case <-timeout.C:
		return
	}
	select {
	case <-ack:
	case <-timeout.C:
	}
}

// setupTail reads the last non-blank lines of the setup window's screen,
// with the script's own heading left out. It returns old when the window
// is gone.
func setupTail(sess *Session, window string, old []string) []string {
	var ptyID string
	for _, w := range sess.GetState().Windows {
		if w.ID == window {
			ptyID = w.PTYID
			break
		}
	}
	pty := sess.GetPTY(ptyID)
	if pty == nil {
		return old
	}
	return lastLines(pty.CaptureContent(false, false), worktreeSetupTailLines)
}

// lastLines is the last n non-blank lines of screen, each cut to
// worktreeSetupTailWidth bytes on a rune boundary, the setup script's
// heading left out.
func lastLines(screen string, n int) []string {
	lines := strings.Split(screen, "\n")
	out := make([]string, 0, n)
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		l := strings.TrimRight(lines[i], " \t\r")
		if l == "" || strings.HasPrefix(l, "tuios setup: ") {
			continue
		}
		if len(l) > worktreeSetupTailWidth {
			cut := worktreeSetupTailWidth
			for cut > 0 && !utf8.RuneStart(l[cut]) {
				cut--
			}
			l = l[:cut]
		}
		out = append(out, l)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// setWorktreeSetup replaces the session's setup record.
func (s *Session) setWorktreeSetup(v *WorktreeSetup) {
	_ = s.mutateState(func(st *SessionState) error {
		if st.Worktree != nil {
			st.Worktree.Setup = v
		}
		return nil
	})
}

// worktreeSetupOrEmpty is a copy of the session's setup record.
func (s *Session) worktreeSetupOrEmpty() WorktreeSetup {
	if wt := s.Worktree(); wt != nil && wt.Setup != nil {
		cp := *wt.Setup
		cp.Tail = append([]string(nil), wt.Setup.Tail...)
		return cp
	}
	return WorktreeSetup{}
}

// setPromptIfWaiting ends a fan prompt that is still waiting, so fan --wait
// and the rail do not wait for an agent that never starts.
func (s *Session) setPromptIfWaiting(status, note string) {
	_ = s.mutateState(func(st *SessionState) error {
		if wt := st.Worktree; wt != nil && PromptWaiting(wt.PromptStatus) {
			wt.PromptStatus, wt.PromptNote = status, note
		}
		return nil
	})
}

// setupReport is a session's setup record as it is reported: one recorded
// as running or pending that no watcher of this daemon runs was ended by a
// restart, and reads as failed with no exit status.
func (d *Daemon) setupReport(s *Session, v *WorktreeSetup) *WorktreeSetup {
	if v == nil || (v.State != SetupRunning && v.State != SetupPending) || d.setups.running(s.ID) {
		return v
	}
	// The watcher writes the finished record and takes its run out of the
	// table in one step, so a record read before that step and a table read
	// after it are the setup finishing. The record is read again.
	if wt := s.Worktree(); wt != nil && wt.Setup != nil {
		if wt.Setup.State != SetupRunning && wt.Setup.State != SetupPending {
			return wt.Setup
		}
		v = wt.Setup
	}
	if v.State == SetupPending {
		// Between the record and the window: the call is still making it.
		return v
	}
	cp := *v
	cp.State = SetupFailed
	cp.Note = "The daemon restarted before the setup finished."
	return &cp
}

// setWorktreeSetupAgent sets the agent stage on the session's setup record.
func (s *Session) setWorktreeSetupAgent(stage string) {
	_ = s.mutateState(func(st *SessionState) error {
		if wt := st.Worktree; wt != nil && wt.Setup != nil {
			cp := *wt.Setup
			cp.Agent = stage
			wt.Setup = &cp
		}
		return nil
	})
}

// startedByLater returns what records the caller's pane as the one that
// started a window. The pane is read now, while the call is live, because
// a sync setup opens the agent's window after the call has returned.
func (d *Daemon) startedByLater(cs *connState) func(window string) {
	ref := d.callerPaneRef(cs)
	return func(window string) {
		if ref == nil || window == "" || ref.Window == window {
			return
		}
		if sess := d.sessionHoldingWindow(window); sess != nil {
			sess.setStartedBy(window, ref)
		}
	}
}

// setupParams are the parameters new-worktree and fan take for a setup.
var setupParams = []verbParam{
	{Name: "setup", Type: "string", Description: "A shell line to run with sh -c in each new worktree, in a window named setup, before the agent or shell starts. It holds no grants. Omit for [worktree] setup in config.toml. An empty string runs none."},
	{Name: "setup_async", Type: "bool", Description: "Start the agent at once beside the setup. Omit for [worktree] setup_async. Without it the agent waits for the setup to pass, and a failed setup removes the new worktree and the branch it made when nothing in them is anyone's work. A failed async setup keeps both."},
}

// setupReturns are what new-worktree returns about a setup.
var setupReturns = []verbParam{
	{Name: "setup", Type: "object", Description: "The setup's record as list-worktrees reports it. Absent when no setup runs."},
	{Name: "setup_window_id", Type: "string", Description: "Id of the setup's window. Absent when no setup runs."},
	{Name: "setup_error", Type: "string", Description: "Why the setup's window could not start. The worktree and the session are kept. Absent otherwise."},
	{Name: "agent_pending", Type: "bool", Description: "True while a sync setup holds the agent. There is then no window_id: the agent's window opens when the setup passes."},
}
