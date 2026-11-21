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

const worktreeSetupWindow = "setup"

const (
	worktreeSetupTailLines = 4
	worktreeSetupTailWidth = 200
)

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

type setupSpec struct {
	Command string
	Async   bool
}

type setupParam struct {
	Setup      *string `json:"setup"`
	SetupAsync *bool   `json:"setup_async"`
}

type worktreeSetups struct {
	cfg     atomic.Pointer[setupSpec]
	waiting atomic.Bool

	mu   sync.Mutex
	runs map[string]*setupRun
}

type setupRun struct {
	window string
	stop   chan struct{}
	once   sync.Once
	hold   *os.File
	holdMu sync.Mutex
}

func (r *setupRun) halt() { r.once.Do(func() { close(r.stop) }) }

func (r *setupRun) letGo() {
	r.holdMu.Lock()
	defer r.holdMu.Unlock()
	if r.hold != nil {
		_ = r.hold.Close()
		r.hold = nil
	}
}

func validSetupCommand(cmd string) bool {
	return len(cmd) <= config.WorktreeSetupMaxBytes && !strings.ContainsRune(cmd, 0)
}

func (t *worktreeSetups) setConfig(cmd string, async bool) {
	cmd = strings.TrimSpace(cmd)
	if !validSetupCommand(cmd) {
		cmd = ""
	}
	t.cfg.Store(&setupSpec{Command: cmd, Async: async})
}

func (t *worktreeSetups) config() setupSpec {
	if c := t.cfg.Load(); c != nil {
		return *c
	}
	return setupSpec{}
}

func (t *worktreeSetups) applyConfig(cmd string, async, byPerson bool) bool {
	cmd = strings.TrimSpace(cmd)
	if byPerson || cmd == "" || cmd == t.config().Command {
		t.setConfig(cmd, async)
		return true
	}
	log.Printf("[worktree] The new setup command in config.toml waits for tuios config apply from outside tuios, or a daemon restart. The setup in force stays.")
	return false
}

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

func (t *worktreeSetups) put(session string, run *setupRun) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.runs == nil {
		t.runs = make(map[string]*setupRun)
	}
	t.runs[session] = run
}

func (t *worktreeSetups) finish(session string, run *setupRun) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.runs[session] != run {
		return false
	}
	delete(t.runs, session)
	return true
}

func (t *worktreeSetups) running(session string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.runs[session] != nil
}

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

type setupLaunch struct {
	sess    *Session
	info    *WorktreeInfo
	created bool
	spec    setupSpec
	env     []string
	agent   func() (WindowState, error)
	onAgent func(*Session, WindowState)
}

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

func (d *Daemon) watchWorktreeSetup(l setupLaunch, run *setupRun, codes <-chan *int, release func()) {
	defer release()
	defer run.letGo()
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
		_, _ = sess.CloseDaemonWindow(run.window, "the daemon, as its worktree setup passed")
	}
}

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
	d.worktreeRemoved(l.sess.Name(), info.Path)
	if !l.created {
		return true, false, "The worktree was removed. The branch " + info.Branch + " was there before, so it is kept."
	}
	if err := worktree.DeleteBranchAt(info.RepoRoot, info.Branch, info.StartCommit); err != nil {
		return true, false, "The worktree was removed. The branch " + info.Branch + " is kept: " + strings.TrimSpace(err.Error())
	}
	return true, true, "The worktree and the branch " + info.Branch + " were removed."
}

func (d *Daemon) followSetupTail(sess *Session, run *setupRun, ptyID string) {
	sub := d.events.subscribe(eventFilter{
		session: sess.Name(),
		sess:    sess,
		ptyID:   ptyID,
		types:   map[string]bool{EventOutput: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)
	timer := time.NewTimer(worktreeSetupTailEvery)
	due := timer.C
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
			tail := setupTail(sess, window, nil)
			if tail == nil {
				continue
			}
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

func (s *Session) setWorktreeSetup(v *WorktreeSetup) {
	_ = s.mutateState(func(st *SessionState) error {
		if st.Worktree != nil {
			st.Worktree.Setup = v
		}
		return nil
	})
}

func (s *Session) worktreeSetupOrEmpty() WorktreeSetup {
	if wt := s.Worktree(); wt != nil && wt.Setup != nil {
		cp := *wt.Setup
		cp.Tail = append([]string(nil), wt.Setup.Tail...)
		return cp
	}
	return WorktreeSetup{}
}

func (s *Session) setPromptIfWaiting(status, note string) {
	_ = s.mutateState(func(st *SessionState) error {
		if wt := st.Worktree; wt != nil && PromptWaiting(wt.PromptStatus) {
			wt.PromptStatus, wt.PromptNote = status, note
		}
		return nil
	})
}

func (d *Daemon) setupReport(s *Session, v *WorktreeSetup) *WorktreeSetup {
	if v == nil || (v.State != SetupRunning && v.State != SetupPending) || d.setups.running(s.ID) {
		return v
	}
	if wt := s.Worktree(); wt != nil && wt.Setup != nil {
		if wt.Setup.State != SetupRunning && wt.Setup.State != SetupPending {
			return wt.Setup
		}
		v = wt.Setup
	}
	if v.State == SetupPending {
		return v
	}
	cp := *v
	cp.State = SetupFailed
	cp.Note = "The daemon restarted before the setup finished."
	return &cp
}

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

var setupParams = []verbParam{
	{Name: "setup", Type: "string", Description: "A shell line to run with sh -c in each new worktree, in a window named setup, before the agent or shell starts. It holds no grants. Omit for [worktree] setup in config.toml. An empty string runs none."},
	{Name: "setup_async", Type: "bool", Description: "Start the agent at once beside the setup. Omit for [worktree] setup_async. Without it the agent waits for the setup to pass, and a failed setup removes the new worktree and the branch it made when nothing in them is anyone's work. A failed async setup keeps both."},
}

var setupReturns = []verbParam{
	{Name: "setup", Type: "object", Description: "The setup's record as list-worktrees reports it. Absent when no setup runs."},
	{Name: "setup_window_id", Type: "string", Description: "Id of the setup's window. Absent when no setup runs."},
	{Name: "setup_error", Type: "string", Description: "Why the setup's window could not start. The worktree and the session are kept. Absent otherwise."},
	{Name: "agent_pending", Type: "bool", Description: "True while a sync setup holds the agent. There is then no window_id: the agent's window opens when the setup passes."},
}
