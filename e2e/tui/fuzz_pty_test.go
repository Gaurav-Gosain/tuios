package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuitest"
)

// The other end of the fuzzer: the same action alphabet, replayed against a real
// tuios in a real PTY with a real daemon behind it.
//
// It used to be the weaker oracle, and it used to boot the binary with no
// arguments, which is the standalone TUI (cmd/tuios/main.go RunE -> runLocal).
// That target had no daemon at all, so the half of tuios this file exists to
// cover was not being covered: every finding it could produce was one the
// in-process target could produce faster. It now attaches to a real session, and
// the oracle asks the daemon what it holds rather than asking the client whether
// it agrees with itself. See daemon_witness_test.go for why that distinction is
// the whole point.
//
// The in-process target still owns everything that can be read off the model:
// hit rectangles, announced pane sizes, layout geometry. Duplicating those here
// against a screen scrape would be slower and less certain about the same
// property. What lives here is what only exists once a socket is involved.
//
// Runs are short and shrinking is off by default. A replay costs a full tuios
// boot plus a daemon plus its shells, so the minimisation that takes seconds in
// process takes minutes here; a PTY finding is reproduced with its script and
// then narrowed in process when the class allows it.

const (
	ptyCols = 120
	ptyRows = 40
	// ptyPanes matches the in-process target's starting pane count, so a script
	// that means something there means the same thing here.
	ptyPanes = 3
	// daemonEvery is how often the rules that cost a subprocess run when nothing
	// has disturbed the transport. The free rules run after every action; these
	// fork a tuios per pane, and running them after all of a drag's motion
	// reports would spend the whole budget interrogating a daemon about a state
	// no action changed.
	daemonEvery = 4
	// scrollTail is how much history the retention rule reads back. Comfortably
	// more than a burst leaves on the visible screen, so the rule is about the
	// scrollback rather than about the grid.
	scrollTail = 400
	// settle is the beat between an action and the assertion. The screen is
	// asynchronous, so without it a check reads the frame from before the action.
	settle = 12 * time.Millisecond
	// structureSettle is how long the client is given to agree with the daemon
	// about the shape of the session before the disagreement is a finding.
	structureSettle = 2 * time.Second
	// spliceSettle is how long a hole in a pane has to survive before it is a
	// splice rather than a frame caught mid-repaint.
	spliceSettle = 1 * time.Second
)

// ptyTarget drives one tuios client against one daemon.
type ptyTarget struct {
	t          *testing.T
	term       *tuitest.Terminal
	base       string // the isolation root, which is also the daemon's identity
	session    string // the session the run starts on
	current    string // the session the client is showing now
	cols, rows int
	// maxRows is the tallest the host has been during this run, which is what
	// bounds how much of a pane's output an erase can hide. See checkScrollback.
	maxRows  int
	last     fuzz.Action
	step     int
	held     int
	detached bool

	// emitted is the highest witness number written into each pane. It is the
	// upper bound the provenance rule uses: a daemon holding a number nobody
	// asked for did not get it from this run.
	emitted map[string]int
	// tail is the highest witness number the daemon has been seen holding for
	// each pane. Scrollback is allowed to grow and never to forget its end.
	tail map[string]int
	// screenSwitched names the panes whose screen this run has moved, by an
	// altscreen action or by a guest write that changes screens. Their history is
	// no longer a question this oracle can ask: output written while a pane was
	// on the alternate screen is discarded when it leaves, so a run that switches
	// screens across a burst leaves a hole the pane really does have and that
	// says nothing about whether any history was lost.
	screenSwitched map[string]bool
	// alt names the panes the daemon has confirmed are on the alternate screen.
	// Entries are added only once confirmed, so the rule is "an alternate screen
	// that existed survives" rather than "an alternate screen was reached",
	// which is a liveness question and is asked in liveness_test.go.
	alt map[string]bool

	// wins and focused are the last structure read from the daemon.
	wins    []daemonWindow
	focused string

	// probe forces the daemon-side rules to run on the next check. The actions
	// that disturb the transport set it, because those are the steps where the
	// answer can have changed.
	probe bool
	// pending are violations an action produced while applying, which cannot be
	// returned from Apply: an error there aborts the run, and a client that
	// failed to come back from a detach is a finding, not a broken harness.
	pending []fuzz.Violation
}

func newPTYTarget(t *testing.T) func() (fuzz.Target, error) {
	return func() (fuzz.Target, error) {
		return &ptyTarget{t: t, session: "fuzz", cols: ptyCols, rows: ptyRows}, nil
	}
}

// Reset builds the starting state: a daemon holding two sessions, three panes on
// the first, and one client attached to it in window-management mode.
//
// The second session exists so a session switch has somewhere to land. A switch
// with nowhere to go is a no-op, and a no-op weighted into the stream is budget
// spent on nothing; a switch that rebuilds every pane on a fresh emulator is one
// of the shapes that has actually broken here.
func (p *ptyTarget) Reset() error {
	p.cols, p.rows, p.held, p.step = ptyCols, ptyRows, 0, 0
	p.maxRows = ptyRows
	p.detached, p.probe, p.pending = false, true, nil
	p.emitted, p.tail = map[string]int{}, map[string]int{}
	p.alt, p.screenSwitched = map[string]bool{}, map[string]bool{}
	p.wins, p.focused = nil, ""
	p.current = p.session

	// A fresh isolation root per replay, so a shrunk sequence starts from the
	// same empty daemon the original did. Registered for teardown before
	// anything creates a daemon under it, because the sessions below start one.
	p.base = p.t.TempDir()
	killDaemon(p.t, p.base)

	for _, name := range []string{p.session, p.session + "-b"} {
		if out, err := tuiosCLI(p.t, p.base, "new", name, "--detach"); err != nil {
			return fmt.Errorf("create session %s: %w: %s", name, err, strings.TrimSpace(out))
		}
	}
	for range ptyPanes - 1 {
		if out, err := tuiosCLI(p.t, p.base, "new-window", "-s", p.session); err != nil {
			return fmt.Errorf("new-window: %w: %s", err, strings.TrimSpace(out))
		}
	}

	p.term = startIn(p.t, p.base, startOpts{
		cols: p.cols, rows: p.rows,
		args: []string{"attach", p.session},
	})
	if err := waitDock(p.t, p.term); err != nil {
		return fmt.Errorf("the client never rehydrated the session: %w", err)
	}
	// An attached client boots into terminal mode, where a plain key is shell
	// input. The alphabet's plain keys are window-manager commands, so the run
	// starts where the in-process target starts and wanders from there.
	p.windowMode()
	if err := p.refresh(); err != nil {
		return err
	}
	// Seed every pane, so the oracle has something to be wrong about from the
	// first action rather than only once the run happens to generate a burst.
	for _, w := range p.wins {
		if err := p.burst(w, 12); err != nil {
			return err
		}
	}
	return nil
}

// Close tears down this replay's client and daemon.
//
// The daemon kill is not left to the cleanup startIn registers. Those all run at
// the end of the test, and a campaign is hundreds of replays, so deferring them
// means hundreds of daemons and their shells alive at once on the machine
// running the suite. The maintainer's machine has been flooded by leaked test
// daemons before.
func (p *ptyTarget) Close() {
	if p.term != nil {
		_ = p.term.Close()
		p.term = nil
	}
	if p.base != "" {
		killDaemonNow(p.t, p.base)
	}
}

// Apply replays one action as real bytes on the wire, or as a real daemon
// operation for the ones that have no keyboard spelling. Actions with a chord
// are sent as the chord a user would press, which is the point of running this
// target at all: it exercises the binding table, not the method behind it.
func (p *ptyTarget) Apply(a fuzz.Action) error {
	p.last = a
	p.step++
	if disturbs(a.Kind) {
		p.probe = true
	}

	switch a.Kind {
	case fuzz.Burst:
		p.applyBurst(a)
	case fuzz.AltScreen:
		p.applyAltScreen(a)
	case fuzz.Guest:
		p.applyGuest(a)
	case fuzz.Detach:
		p.applyDetach()
	case fuzz.Attach:
		p.applyAttach()
	case fuzz.SecondClient:
		p.applySecondClient()
	case fuzz.DaemonRestart:
		p.applyDaemonRestart()
	default:
		if err := p.applyToClient(a); err != nil {
			// A write that fails because the client is gone is not a broken
			// harness, it is the finding: the alphabet contains quit chords, and
			// returning the error here would abort the whole campaign on the
			// first seed that pressed one instead of letting pty-exit report it
			// with the script that caused it.
			if _, exited := p.term.ExitCode(); !exited {
				return err
			}
		}
	}
	if p.probe {
		_ = p.refresh()
	}
	time.Sleep(settle)
	return nil
}

// disturbs names the actions after which the daemon-side rules are worth their
// subprocesses: everything that moves a pane between rendered and not, everything
// that changes which client is reading the stream, and everything that produces
// output.
func disturbs(k fuzz.Kind) bool {
	switch k {
	case fuzz.Burst, fuzz.AltScreen, fuzz.Guest, fuzz.Detach, fuzz.Attach,
		fuzz.SecondClient, fuzz.DaemonRestart, fuzz.SwitchWorkspace,
		fuzz.SwitchSession, fuzz.NewPane, fuzz.ClosePane, fuzz.Resize:
		return true
	}
	return false
}

// applyToClient sends the action as input to the attached client.
func (p *ptyTarget) applyToClient(a fuzz.Action) error {
	t := p.term
	if t == nil {
		// Detached. There is no client to type at, and the daemon rules still
		// run, which is the interesting half of a detached step.
		return nil
	}
	switch a.Kind {
	case fuzz.Key:
		return t.SendKeys(ptyKey(a.S))
	case fuzz.Chord:
		return t.SendKeys(tuitest.Ctrl('b'), ptyKey(a.S))
	case fuzz.Text:
		return t.Type(a.S)
	case fuzz.MousePress:
		p.held = a.C
		return t.SendMouse(p.mouse(a, tuitest.MousePress))
	case fuzz.MouseMotion:
		return t.SendMouse(p.mouse(a, tuitest.MouseMove))
	case fuzz.MouseRelease:
		p.held = 0
		return t.SendMouse(p.mouse(a, tuitest.MouseRelease))
	case fuzz.MouseWheel:
		return t.SendMouse(p.mouse(a, tuitest.MousePress))
	case fuzz.Resize:
		// The PTY refuses a zero dimension, and the degenerate-viewport class is
		// the in-process target's to hunt; here the floor keeps the run exploring
		// instead of wedging on an ioctl error.
		p.cols, p.rows = max(a.A, 20), max(a.B, 6)
		p.maxRows = max(p.maxRows, p.rows)
		return t.Resize(p.cols, p.rows)
	case fuzz.NewPane:
		return t.SendKeys(tuitest.Ctrl('b'), "c")
	case fuzz.ClosePane:
		return t.SendKeys(tuitest.Ctrl('b'), "x")
	case fuzz.ZoomPane:
		return t.SendKeys(tuitest.Ctrl('b'), "z")
	case fuzz.FocusPane:
		return t.SendKeys(tuitest.Ctrl('b'), "n")
	case fuzz.MovePane:
		return t.SendKeys([]string{"left", "right", "up", "down"}[a.A%4])
	case fuzz.SwitchWorkspace:
		return t.SendKeys(tuitest.Ctrl('b'), "w", strconv.Itoa(1+a.A%9))
	case fuzz.SwitchSession:
		// next_session is terminal-safe, so it works whichever mode the run has
		// wandered into. Which session it lands on is read back from the daemon
		// rather than assumed, because the walk order is the daemon's.
		if err := t.SendKeys(tuitest.Alt("N")); err != nil {
			return err
		}
		p.current = attachedSession(p.base, p.current)
		return nil
	case fuzz.ToggleTiling:
		return t.SendKeys(tuitest.Ctrl('b'), " ")
	case fuzz.LayoutMode:
		return t.SendKeys(tuitest.Ctrl('b'), "L")
	case fuzz.ToggleSidebar, fuzz.SidebarCollapse, fuzz.SidebarPosition:
		return t.SendKeys(tuitest.Ctrl('b'), "b")
	case fuzz.OpenOverlay:
		return t.SendKeys(tuitest.Ctrl('b'), []string{"?", "P", "S", "W", ","}[a.A%5])
	case fuzz.CloseOverlay:
		return t.SendKeys("esc")
	case fuzz.Rename:
		if err := t.SendKeys(tuitest.Ctrl('b'), "r"); err != nil {
			return err
		}
		if err := t.Type(sanitiseName(a.S)); err != nil {
			return err
		}
		return t.SendKeys("enter")
	case fuzz.ToggleShared, fuzz.Setting:
		// Both live behind the settings panel rather than a chord. The
		// in-process target flips them directly and owns that class.
		return nil
	case fuzz.Tick:
		// Time passes on its own here, because the real timers are running. The
		// beat is what a tick means in this target.
		time.Sleep(60 * time.Millisecond)
		return nil
	}
	return nil
}

// applyBurst makes one pane print a run of witness lines. A is how many, B picks
// the pane, and the pane is picked by index rather than by focus on purpose: a
// pane printing while nobody is rendering it is the case the catch-up ring is
// for.
func (p *ptyTarget) applyBurst(a fuzz.Action) {
	w, ok := p.pickWindow(a.B)
	if !ok {
		return
	}
	if err := p.burst(w, a.A); err != nil {
		p.note("burst", "pane %s refused %d lines: %v", w.tag(), a.A, err)
	}
}

// applyAltScreen puts a pane on the alternate screen or takes it off, and only
// records the expectation once the daemon confirms the pane is really there.
func (p *ptyTarget) applyAltScreen(a fuzz.Action) {
	w, ok := p.pickWindow(a.B)
	if !ok {
		return
	}
	enter := a.A%2 == 1
	p.screenSwitched[w.ID] = true
	if err := paneSend(p.base, p.current, w.ID, paneAltCmd(w.tag(), enter)); err != nil {
		return
	}
	if !enter {
		delete(p.alt, w.ID)
		return
	}
	// Confirmed rather than assumed: the pane may be busy, and an expectation
	// recorded before the pane acted would be reported as a lost alternate
	// screen on the very next check.
	want := "ALT" + w.tag()
	for range 20 {
		time.Sleep(50 * time.Millisecond)
		lines, err := daemonPane(p.base, p.current, w.ID)
		if err != nil {
			return
		}
		if strings.Contains(strings.Join(lines, "\n"), want) {
			p.alt[w.ID] = true
			return
		}
	}
}

// applyGuest makes the focused pane's own program emit the generated bytes,
// escapes included, rather than typing them at the client. Typed at the client
// they would be keys; emitted by the pane they are what a program does to its
// terminal, which is what the pool is for.
func (p *ptyTarget) applyGuest(a fuzz.Action) {
	w, ok := p.focusedWindow()
	if !ok {
		return
	}
	// A guest write can switch screens by itself, and the pool is full of writes
	// that do. Any expectation about this pane's alternate screen is no longer
	// this run's to hold, and neither is any question about its history.
	delete(p.alt, w.ID)
	p.screenSwitched[w.ID] = true
	_ = paneSend(p.base, p.current, w.ID, paneEmitCmd(a.S))
}

func (p *ptyTarget) applyDetach() {
	if p.detached || p.term == nil {
		return
	}
	if err := p.term.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		return
	}
	if _, err := p.term.Wait(uiTimeout); err != nil {
		// Still attached, and that is not reported. Whether the leader chord
		// reaches tuios depends on the mode the run has wandered into and on
		// what the focused pane is doing with the keyboard, so a detach that
		// does not happen here is a statement about the state the fuzzer built
		// and not about detaching. The claim that detaching works is made from a
		// known state, by TestLivenessReattachRestoresWhatWasThere.
		//
		// What matters is that the target does not now believe it is detached: a
		// wrong answer there would point every later rule at the wrong client.
		return
	}
	_ = p.term.Close()
	p.term, p.detached = nil, true
}

func (p *ptyTarget) applyAttach() {
	if !p.detached {
		return
	}
	p.term = startIn(p.t, p.base, startOpts{
		cols: p.cols, rows: p.rows,
		args: []string{"attach", p.current},
	})
	if err := waitDock(p.t, p.term); err != nil {
		p.note("attach", "reattaching to %q never produced a dock: %v", p.current, err)
		return
	}
	p.detached = false
	p.windowMode()
}

// applySecondClient attaches another client to the live session, lets it
// rehydrate, and takes it away again.
//
// Its size is deliberately different. tuios renders a shared session at the
// smallest attached client's size, so a second client is also the size
// negotiation path, and the first client has to survive being resized by
// somebody else's arrival and departure.
func (p *ptyTarget) applySecondClient() {
	other := startIn(p.t, p.base, startOpts{
		cols: max(p.cols-13, 40), rows: max(p.rows-5, 12),
		args: []string{"attach", p.current},
	})
	if err := waitDock(p.t, other); err != nil {
		p.note("second-client", "a second client on %q never rehydrated: %v", p.current, err)
	}
	_ = other.Close()
	// The first client is now alone again and has to be told so.
	time.Sleep(200 * time.Millisecond)
}

// applyDaemonRestart takes the daemon away and brings it back, which is the
// restore path: kill-server writes the session state and waits for the socket to
// go, and the next attach starts a daemon that reads it back.
func (p *ptyTarget) applyDaemonRestart() {
	if out, err := tuiosCLI(p.t, p.base, "kill-server"); err != nil {
		p.note("daemon-restart", "kill-server failed: %v: %s", err, strings.TrimSpace(out))
	}
	if p.term != nil {
		if _, err := p.term.Wait(uiTimeout); err != nil {
			p.note("daemon-restart", "the client outlived its daemon by %s", uiTimeout)
		}
		_ = p.term.Close()
		p.term = nil
	}
	p.detached = true

	p.term = startIn(p.t, p.base, startOpts{
		cols: p.cols, rows: p.rows,
		args: []string{"attach", p.current},
	})
	if err := waitDock(p.t, p.term); err != nil {
		p.note("daemon-restart", "%q did not come back after a restart: %v", p.current, err)
		return
	}
	p.detached = false
	p.windowMode()
	// Restore rebuilds the panes on new shells, so their history is genuinely
	// gone and the retention baseline is retired rather than reported as a loss.
	// Whether a restored pane should keep its scrollback is a product question
	// this target is not entitled to answer.
	p.tail, p.alt = map[string]int{}, map[string]bool{}
}

// Check is the oracle. It runs in two tiers.
//
// The free tier reads only the client's screen and runs after every action: the
// process is alive, no panic reached the screen, the grid is the size the PTY
// is, and no pane is showing a stream spliced across a hole. That last one is
// the rule this file was rewritten for, and it costs nothing, which is why it
// gets to run on every step rather than on a sample of them.
//
// The daemon tier forks a tuios per pane and runs when an action disturbed the
// transport or every daemonEvery steps otherwise. It is the half that can tell
// a client that is merely behind from a client that is wrong.
func (p *ptyTarget) Check() []fuzz.Violation {
	if vs := p.pending; len(vs) > 0 {
		p.pending = nil
		return vs
	}
	if vs := p.checkClient(); len(vs) > 0 {
		return vs
	}
	if !p.probe && p.step%daemonEvery != 0 {
		return nil
	}
	p.probe = false
	return p.checkDaemon()
}

func (p *ptyTarget) checkClient() []fuzz.Violation {
	t := p.term
	if t == nil {
		// Detached on purpose. An exited client is the state, not a finding.
		return nil
	}
	if code, exited := t.ExitCode(); exited {
		// A clean exit is the run having pressed something that ends a client,
		// and the chord pool has several: ctrl+b d detaches, ctrl+b q quits, and
		// closing the last pane can take the session with it. Enumerating them
		// would be a table that goes stale the next time a binding is added, and
		// the exit code already draws the line the rule wants: zero is tuios
		// deciding to stop, anything else is tuios being stopped. Two seeds were
		// reported as pty-exit for pressing detach.
		//
		// The client is gone either way, so the target records that. The daemon
		// rules keep running against a session with nobody watching it, which is
		// the more interesting half of the step, and an Attach brings it back.
		if code == 0 {
			p.term, p.detached = nil, true
			return nil
		}
		return one("pty-exit", "tuios exited with code %d after %s", code, p.last)
	}
	s := t.Screen()
	text := s.Text()
	// A Go runtime panic reaches the screen before the process dies, and it is
	// the one failure a user reports as "it vanished".
	for _, marker := range []string{"panic:", "goroutine ", "runtime error:", "SIGSEGV"} {
		if strings.Contains(text, marker) {
			return one("pty-panic", "the screen shows %q after %s", marker, p.last)
		}
	}
	cols, rows := s.Size()
	if cols != p.cols || rows != p.rows {
		return one("pty-size", "the grid is %dx%d, the PTY is %dx%d", cols, rows, p.cols, p.rows)
	}
	if a, b, found := spliceIn(screenLines(s)); found {
		// Confirmed over a beat, because a frame caught halfway through a
		// repaint has the same shape: old rows above new ones with the middle
		// not yet drawn. That is worth knowing and is not this rule, which is
		// about a pane that comes back wrong and stays wrong. A hole still there
		// a second later is not a frame in progress.
		if a2, b2, still := p.spliceHolds(); still {
			return one("client-splice",
				"pane %s shows line %d directly above line %d, still there after %s, "+
					"so the client replayed a stream across a hole rather than "+
					"catching a repaint mid-frame; first seen as %d above %d after %s",
				a2.tag, a2.seq, b2.seq, spliceSettle, a.seq, b.seq, p.last)
		}
	}
	return nil
}

// spliceHolds re-reads the client's screen until a splice goes away or the
// budget runs out.
func (p *ptyTarget) spliceHolds() (witness, witness, bool) {
	deadline := time.Now().Add(spliceSettle)
	var a, b witness
	for {
		x, y, found := spliceIn(screenLines(p.term.Screen()))
		if !found {
			return witness{}, witness{}, false
		}
		a, b = x, y
		if !time.Now().Before(deadline) {
			return a, b, true
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (p *ptyTarget) checkDaemon() []fuzz.Violation {
	if _, err := daemonInfo(p.base, p.current); err != nil {
		return one("daemon-reachable", "session-info for %q failed after %s: %v",
			p.current, p.last, err)
	}
	wl, err := daemonWindows(p.base, p.current)
	if err != nil {
		return one("daemon-reachable", "list-windows for %q failed after %s: %v",
			p.current, p.last, err)
	}
	p.wins, p.focused = wl.Windows, wl.FocusedWindowID

	if vs := p.checkStructure(); len(vs) > 0 {
		return vs
	}
	if vs := p.checkPanes(); len(vs) > 0 {
		return vs
	}
	return p.checkProvenance()
}

// checkStructure compares the two numbers the client puts in its dock against
// the daemon's own answer for the same two. It is the cheapest true client
// against daemon comparison there is.
//
// It is stated as convergence rather than as an instant. The client repaints on
// its own schedule and the daemon answers immediately, so a snapshot comparison
// reports every frame of lag as a disagreement; what is actually being claimed
// is that the client ends up agreeing, and a client that never does is the
// finding. Both sides are re-read each round, because both move.
//
// Two things make it decline to compare rather than fail. A dock that is not on
// screen gives -1, and a workspace number outside the session's own range is the
// status regex having matched something else: at 40x12 the dock is squeezed to
// nothing and any "n:m" in a pane's output can win the row. A comparison against
// a misread is not a pass, and it is not a failure either.
func (p *ptyTarget) checkStructure() []fuzz.Violation {
	if p.term == nil {
		return nil
	}
	deadline := time.Now().Add(structureSettle)
	var wantWS, gotWS, wantWins, gotWins int
	for {
		s := p.term.Screen()
		gotWins, gotWS = countWindows(s), dockWorkspace(s)
		info, err := daemonInfo(p.base, p.current)
		if err != nil {
			return nil
		}
		wantWS = info.CurrentWorkspace
		if gotWins < 0 || gotWS < 1 || gotWS > max(info.NumWorkspaces, 1) {
			return nil
		}
		wl, err := daemonWindows(p.base, p.current)
		if err != nil {
			return nil
		}
		wantWins = 0
		for _, w := range wl.Windows {
			if w.Workspace == wantWS {
				wantWins++
			}
		}
		if gotWS == wantWS && gotWins == wantWins {
			return nil
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(120 * time.Millisecond)
	}
	if gotWS != wantWS {
		return one("daemon-workspace",
			"the dock still says workspace %d after %s and the daemon says %d, following %s",
			gotWS, structureSettle, wantWS, p.last)
	}
	return one("daemon-window-count",
		"the dock still counts %d windows on workspace %d after %s and the daemon "+
			"lists %d, following %s", gotWins, wantWS, structureSettle, wantWins, p.last)
}

// checkPanes asks the daemon what each pane holds.
func (p *ptyTarget) checkPanes() []fuzz.Violation {
	for _, w := range p.wins {
		grid, err := daemonPane(p.base, p.current, w.ID)
		if err != nil {
			// A pane closed between the list and the capture is a race in the
			// reading, not a finding about tuios.
			continue
		}
		if a, b, found := spliceIn(grid); found {
			return one("daemon-splice",
				"the daemon's own grid for pane %s has line %d directly above line %d "+
					"after %s, so the hole is in the daemon rather than in the client",
				a.tag, a.seq, b.seq, p.last)
		}
		if vs := p.checkAlt(w, grid); len(vs) > 0 {
			return vs
		}
		if vs := p.checkScrollback(w, grid); len(vs) > 0 {
			return vs
		}
	}
	return nil
}

// checkAlt holds a pane to an alternate screen it was seen to have. A pane that
// drops back to the main screen on its own is the pane whose program is still
// running and whose display is now somebody else's.
func (p *ptyTarget) checkAlt(w daemonWindow, grid []string) []fuzz.Violation {
	if !p.alt[w.ID] {
		return nil
	}
	joined := strings.Join(grid, "\n")
	if !altWitnessRe.MatchString(joined) {
		return one("altscreen-retained",
			"pane %s was on the alternate screen and its marker is gone after %s",
			w.tag(), p.last)
	}
	if _, _, any := seqRange(grid, w.tag()); any {
		return one("altscreen-retained",
			"pane %s is on the alternate screen and the daemon's grid for it also "+
				"carries main-screen output after %s", w.tag(), p.last)
	}
	return nil
}

// checkScrollback holds the daemon's history to two rules: its end never moves
// far backwards, and it is adjacent to itself.
//
// The first is what a session switch, a detach and a workspace change are
// allowed to do to a pane, which is nothing. The second is the same splice rule
// applied to history rather than to the visible grid, which is where a hole ends
// up once the pane has scrolled past it.
//
// "Far" is one screen, and that slack is not a hedge. A scrollback capture is
// the history plus whatever is on the screen right now, so anything that empties
// the screen without touching the history - an erase, or a switch to the
// alternate screen, both of which the guest pool generates - legitimately takes
// the last screenful off the end of the answer. Losing more than a screen is
// losing history, and that is the thing worth reporting.
func (p *ptyTarget) checkScrollback(w daemonWindow, grid []string) []fuzz.Violation {
	if p.emitted[w.ID] == 0 {
		return nil
	}
	// Only while the pane is showing its main screen. The alternate screen has
	// no scrollback of its own, so a capture taken there answers with the
	// alternate screen's empty history and not with the main screen's, and the
	// rule cannot tell that apart from history genuinely thrown away. A pane
	// with none of its own output on screen is in that state or an equivalent
	// one, and the honest move is to decline rather than to guess: it reported
	// two seeds as losing history when all they had done was switch screens.
	if _, _, any := seqRange(grid, w.tag()); !any {
		return nil
	}
	// And not for a pane whose screen this run has moved. See screenSwitched:
	// output written to a pane on the alternate screen goes away with it, so a
	// burst that straddles a switch leaves a hole the pane genuinely has.
	if p.screenSwitched[w.ID] {
		return nil
	}
	hist, err := daemonScrollback(p.base, p.current, w.ID, scrollTail)
	if err != nil {
		return nil
	}
	if a, b, found := spliceIn(hist); found {
		return one("scrollback-retained",
			"pane %s has line %d directly above line %d in its history after %s",
			a.tag, a.seq, b.seq, p.last)
	}
	_, hi, any := seqRange(hist, w.tag())
	if !any {
		return nil
	}
	// The screenful of slack is the largest this pane has ever been, not the
	// size it is now. A run that fills a 64 row pane, hides its screen and then
	// shrinks to 12 rows has legitimately taken 64 rows off the end of the
	// answer, and sizing the tolerance from the current height reported that as
	// lost history twice.
	screenful := max(w.Height, p.maxRows)
	if screenful <= 0 {
		screenful = p.rows
	}
	if hi < p.tail[w.ID]-screenful-2 {
		return one("scrollback-retained",
			"pane %s held history up to line %d and now ends at %d after %s, "+
				"which is more than the screenful an erase could account for",
			w.tag(), p.tail[w.ID], hi, p.last)
	}
	if hi > p.tail[w.ID] {
		p.tail[w.ID] = hi
	}
	return nil
}

// checkProvenance holds both sides to what this run actually wrote.
//
// The daemon may not hold a number nobody produced, and the client may not show
// a number the daemon does not have. The second is the case a self-consistent
// splice hides in: a run of lines that ascends correctly can still be the wrong
// run of lines, and the only way to know is to ask the side that owns the
// original.
func (p *ptyTarget) checkProvenance() []fuzz.Violation {
	if p.term == nil {
		return nil
	}
	client := screenLines(p.term.Screen())
	for _, w := range p.wins {
		tag := w.tag()
		_, clientHi, any := seqRange(client, tag)
		if !any {
			continue
		}
		if clientHi > p.emitted[w.ID] {
			return one("witness-provenance",
				"the client shows pane %s at line %d and only %d were ever written, after %s",
				tag, clientHi, p.emitted[w.ID], p.last)
		}
		grid, err := daemonPane(p.base, p.current, w.ID)
		if err != nil {
			continue
		}
		_, daemonHi, ok := seqRange(grid, tag)
		if !ok {
			continue
		}
		// The daemon is read after the client, so it leads: it may be ahead and
		// never behind. Being behind means the client painted something the
		// daemon does not have.
		//
		// The mirror of this, a client whose lowest line is older than the
		// daemon's, is deliberately not a rule. A client that is merely a few
		// frames behind on a scrolling pane produces exactly that signature, so
		// it would report lag as corruption. The splice rule is what catches the
		// stale case, and it catches it without needing to know how far behind
		// the client is entitled to be.
		if clientHi > daemonHi {
			return one("client-ahead",
				"the client shows pane %s up to line %d, the daemon's grid ends at %d, after %s",
				tag, clientHi, daemonHi, p.last)
		}
	}
	return nil
}

// Rules names this target's oracle, in the order Check applies them.
func (p *ptyTarget) Rules() []string {
	return []string{
		"pty-exit", "pty-panic", "pty-size", "client-splice",
		"daemon-reachable", "daemon-workspace", "daemon-window-count",
		"daemon-splice", "altscreen-retained", "scrollback-retained",
		"witness-provenance", "client-ahead",
		"burst", "attach", "second-client", "daemon-restart",
	}
}

// ---------------------------------------------------------------------------
// Helpers

func (p *ptyTarget) note(rule, format string, args ...any) {
	p.pending = append(p.pending, fuzz.Violation{
		Rule:   rule,
		Detail: fmt.Sprintf(format, args...),
	})
}

func one(rule, format string, args ...any) []fuzz.Violation {
	return []fuzz.Violation{{Rule: rule, Detail: fmt.Sprintf(format, args...)}}
}

func (p *ptyTarget) refresh() error {
	wl, err := daemonWindows(p.base, p.current)
	if err != nil {
		return err
	}
	p.wins, p.focused = wl.Windows, wl.FocusedWindowID
	return nil
}

func (p *ptyTarget) pickWindow(i int) (daemonWindow, bool) {
	if len(p.wins) == 0 && p.refresh() != nil {
		return daemonWindow{}, false
	}
	if len(p.wins) == 0 {
		return daemonWindow{}, false
	}
	n := len(p.wins)
	return p.wins[((i%n)+n)%n], true
}

func (p *ptyTarget) focusedWindow() (daemonWindow, bool) {
	for _, w := range p.wins {
		if w.ID == p.focused {
			return w, true
		}
	}
	return p.pickWindow(0)
}

// burst writes n more witness lines into a pane, continuing that pane's own
// numbering. Continuing rather than restarting is what makes two bursts
// separated by anything at all still adjacent where they meet, so the adjacency
// rule keeps meaning the same thing for the whole run.
func (p *ptyTarget) burst(w daemonWindow, n int) error {
	n = min(max(n, 1), 5000)
	// Output written to a pane that is on the alternate screen lands on the
	// alternate screen, so the pane now carries witness lines there and the
	// alternate-screen rule can no longer tell it apart from a pane that fell
	// back to the main one. The expectation is retired rather than reported.
	delete(p.alt, w.ID)
	lo := p.emitted[w.ID] + 1
	hi := lo + n - 1
	p.emitted[w.ID] = hi
	return paneSend(p.base, p.current, w.ID, paneWitnessCmd(w.tag(), lo, hi))
}

// windowMode settles the client in window-management mode. It is best effort:
// the run is allowed to wander into any mode, and a client that will not settle
// is a finding the rules will report rather than something Reset should assert.
func (p *ptyTarget) windowMode() {
	if p.term == nil {
		return
	}
	_ = p.term.SendKeys(tuitest.Alt(tuitest.Esc))
	_ = p.term.WaitForText("Window Management Mode", uiTimeout)
	time.Sleep(insertGuard)
}

// attachedSession reports which session the client is now showing, by asking the
// daemon which one has a client on it. It is how the target follows a session
// switch without assuming the walk order, which belongs to the daemon.
//
// Getting this wrong is worse than a missed switch: every later rule would
// compare the client's screen against a different session's state and report the
// mismatch as a finding. So it polls until the answer moves, and it prefers the
// session it was already on whenever that one is still attached, because during
// a switch both ends can briefly claim a client.
// The answer it waits for is a session that is attached and is not the one the
// run was already on. Waiting for "exactly one attached" instead is the bug this
// replaced: that is already true the instant the key is sent, before the client
// has done anything, so it returned the old name every time and every later rule
// compared the new session's screen against the old session's state. It reported
// three seeds as daemon-workspace and daemon-window-count failures that way, one
// of them in three actions.
func attachedSession(base, current string) string {
	deadline := time.Now().Add(4 * time.Second)
	for {
		for _, name := range attachedSessions(base) {
			if name != current {
				return name
			}
		}
		if !time.Now().Before(deadline) {
			// The walk has nowhere to go, or it did not happen. Either way the
			// run is still on the session it was on.
			return current
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func attachedSessions(base string) []string {
	var sessions []struct {
		Name     string `json:"name"`
		Attached bool   `json:"attached"`
	}
	out, err := tuiosOut(base, "ls", "--json")
	if err != nil || json.Unmarshal([]byte(out), &sessions) != nil {
		return nil
	}
	var names []string
	for _, s := range sessions {
		if s.Attached {
			names = append(names, s.Name)
		}
	}
	return names
}

func (p *ptyTarget) mouse(a fuzz.Action, action tuitest.MouseAction) tuitest.MouseEvent {
	return tuitest.MouseEvent{
		Col:    min(max(a.A, 0), p.cols-1),
		Row:    min(max(a.B, 0), p.rows-1),
		Button: ptyButton(a.C),
		Action: action,
	}
}

func ptyButton(c int) tuitest.MouseButton {
	switch c {
	case fuzz.ButtonRight:
		return tuitest.MouseRight
	case fuzz.ButtonMiddle:
		return tuitest.MouseMiddle
	}
	return tuitest.MouseLeft
}

// ptyKey maps a key name onto what tuitest sends. Modified names are spelled
// out; everything else goes as its own text.
func ptyKey(name string) any {
	if rest, ok := strings.CutPrefix(name, "ctrl+"); ok && len(rest) == 1 {
		return tuitest.Ctrl(rune(rest[0]))
	}
	if rest, ok := strings.CutPrefix(name, "alt+"); ok {
		return tuitest.Alt(rest)
	}
	if rest, ok := strings.CutPrefix(name, "shift+"); ok {
		return strings.ToUpper(rest)
	}
	if name == "space" {
		return " "
	}
	return name
}

// sanitiseName drops the control characters from a generated name. They are the
// point of the pool in process, where the name reaches the width table directly;
// sent down a PTY they would be read as keys and the run would stop meaning what
// its script says.
func sanitiseName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r != 0x7f {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "a"
	}
	return b.String()
}

// ptyWeights is this target's share of the alphabet.
//
// It is not the in-process weighting and should not be. Everything expressible
// in process is already fuzzed there thousands of times faster, so spending a
// PTY step on a mouse motion buys almost nothing; what only exists here is the
// socket, so the stream leans on the actions that move bytes across it or move a
// client on and off it. The zeros are actions this target genuinely cannot
// express: the settings behind the settings panel have no chord.
func ptyWeights() []int {
	w := make([]int, int(fuzz.DaemonRestart)+1)
	w[fuzz.Key] = 40
	w[fuzz.Chord] = 70
	w[fuzz.Text] = 0
	w[fuzz.MousePress] = 30
	w[fuzz.MouseMotion] = 40
	w[fuzz.MouseRelease] = 30
	w[fuzz.MouseWheel] = 15
	w[fuzz.Resize] = 45
	w[fuzz.NewPane] = 35
	w[fuzz.ClosePane] = 12
	w[fuzz.ZoomPane] = 18
	w[fuzz.FocusPane] = 25
	w[fuzz.MovePane] = 15
	w[fuzz.SwitchWorkspace] = 55
	w[fuzz.SwitchSession] = 25
	w[fuzz.ToggleTiling] = 30
	w[fuzz.ToggleShared] = 0
	w[fuzz.LayoutMode] = 18
	w[fuzz.ToggleSidebar] = 18
	w[fuzz.SidebarCollapse] = 0
	w[fuzz.SidebarPosition] = 0
	w[fuzz.OpenOverlay] = 20
	w[fuzz.CloseOverlay] = 18
	w[fuzz.Rename] = 12
	w[fuzz.Detach] = 20
	w[fuzz.Attach] = 20
	w[fuzz.Setting] = 0
	w[fuzz.Tick] = 20
	w[fuzz.Guest] = 35
	w[fuzz.AltScreen] = 30
	w[fuzz.Burst] = 55
	w[fuzz.SecondClient] = 22
	w[fuzz.DaemonRestart] = 6
	return w
}

// TestFuzzPTY is the bounded PTY campaign:
//
//	cd e2e/tui && TUIOS_E2E=1 go test -count=1 -run TestFuzzPTY ./...
//
// Seeds and steps are settable so a local run can go wider:
//
//	TUIOS_E2E=1 TUIOS_FUZZ_SEEDS=200 TUIOS_FUZZ_STEPS=120 \
//	  go test -count=1 -run TestFuzzPTY -timeout 4h ./...
//
// TUIOS_FUZZ_FIRST moves the starting seed, which is what makes a wide campaign
// survivable. tuitest panics on a scroll region wider than the screen (see the
// third footgun in harness_test.go) and a panic in its pump goroutine takes the
// test binary down with every finding it had not yet printed. Batches of ten
// cost one batch when that happens instead of the whole run:
//
//	for f in 0 10 20 30; do
//	  TUIOS_E2E=1 TUIOS_FUZZ_FIRST=$f TUIOS_FUZZ_SEEDS=10 \
//	    go test -count=1 -run TestFuzzPTY -timeout 1h ./...
//	done
func TestFuzzPTY(t *testing.T) {
	first := uint64(ptyEnvInt(t, "TUIOS_FUZZ_FIRST", 0))
	seeds := ptyEnvInt(t, "TUIOS_FUZZ_SEEDS", 2)
	steps := ptyEnvInt(t, "TUIOS_FUZZ_STEPS", 40)
	shrink := os.Getenv("TUIOS_FUZZ_SHRINK") != ""

	for i := range uint64(seeds) {
		seed := first + i
		res, err := fuzz.Run(newPTYTarget(t), fuzz.Config{
			Seed: seed, Steps: steps,
			MinWidth: 40, MinHeight: 12,
			Weights:  ptyWeights(),
			NoShrink: !shrink, ShrinkBudget: 40,
		})
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if res.Failed {
			t.Errorf("seed %d broke %s\n%s", seed, res.Violations[0].Rule, res.Repro())
		}
	}
}

func ptyEnvInt(t *testing.T, name string, def int) int {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("%s=%q: %v", name, v, err)
	}
	return n
}
