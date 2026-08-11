package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// ptySpy records the size the daemon PTY has actually been told, the way the
// real ResizePTY round trip would leave it.
type ptySpy struct {
	w, h  int
	calls int
}

func (p *ptySpy) install(win *terminal.Window) {
	p.w, p.h = win.ContentWidth(), win.ContentHeight()
	win.DaemonResizeFunc = func(w, h int) error {
		p.w, p.h = w, h
		p.calls++
		return nil
	}
}

// newTilingClient is an attached client with a known viewport, tiling on, BSP.
func newTilingClient(width, height, workspace int) *OS {
	return &OS{
		NumWorkspaces:        9,
		CurrentWorkspace:     workspace,
		WorkspaceFocus:       make(map[int]int),
		WorkspaceLayouts:     make(map[int][]WindowLayout),
		WorkspaceMasterRatio: make(map[int]float64),
		WorkspaceHasCustom:   make(map[int]bool),
		Width:                width,
		Height:               height,
		AutoTiling:           true,
		UseBSPLayout:         true,
	}
}

// daemonNewWindow appends the box AddDaemonWindow produces: a nominal full-size
// window flagged Unplaced, because the daemon has no viewport to place it in.
func daemonNewWindow(state *session.SessionState, id string, width, height, workspace int) {
	state.Windows = append(state.Windows, session.WindowState{
		ID:        id,
		PTYID:     "pty-" + id,
		Title:     id,
		X:         0,
		Y:         0,
		Width:     width,
		Height:    height,
		Workspace: workspace,
		Unplaced:  true,
	})
	state.FocusedWindowID = id
	state.Version++
}

// TestNewWindowPTYMatchesPaneAfterRepeatedUnplacedSync is the reported bug: a
// pane that is a whole screen wide runs its shell at a fraction of that size, so
// a full-screen program paints only the top-left corner of it.
//
// The daemon re-broadcasts the creating state more than once, and the repeats
// still carry Unplaced until this client's placing push lands (the case
// placeUnplacedWindows and the `placed` retile in ApplyStateSync exist for). A
// repeat therefore resizes the real PTY back down to the raw placement box, and
// the retile that follows puts the window back at full size. If that retile
// decides it has nothing to announce, the emulator goes to full size and the PTY
// is left at the placement box: the pane and its shell disagree, permanently,
// because nothing resizes again until the user does.
func TestNewWindowPTYMatchesPaneAfterRepeatedUnplacedSync(t *testing.T) {
	prevAnim := config.AnimationsEnabled
	config.AnimationsEnabled = false
	t.Cleanup(func() { config.AnimationsEnabled = prevAnim })

	const width, height = 130, 55

	m := newTilingClient(width, height, 1)
	daemonState := &session.SessionState{
		Name:             "tiling",
		CurrentWorkspace: 1,
		AutoTiling:       true,
		WorkspaceFocus:   map[int]string{},
		Version:          1,
	}
	daemonNewWindow(daemonState, "win-00000000000000000000000000000001", width, height, 1)

	if err := m.ApplyStateSync(daemonState); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if len(m.Windows) != 1 {
		t.Fatalf("client holds %d windows, want 1", len(m.Windows))
	}
	win := m.Windows[0]

	// The client has placed and tiled the window; the daemon PTY carries that
	// size. Start watching from there.
	var pty ptySpy
	pty.install(win)

	// The daemon re-broadcasts the creating state: same window, still Unplaced,
	// newer version.
	daemonState.Version++
	if err := m.ApplyStateSync(daemonState); err != nil {
		t.Fatalf("echo sync: %v", err)
	}

	wantW, wantH := win.ContentWidth(), win.ContentHeight()
	if pty.w != wantW || pty.h != wantH {
		t.Fatalf("daemon PTY is %dx%d but the pane is %dx%d: the shell paints into a corner of its pane",
			pty.w, pty.h, wantW, wantH)
	}
	if got, want := win.Terminal.Width(), wantW; got != want {
		t.Fatalf("local emulator width %d, want %d", got, want)
	}
	if got, want := win.Terminal.Height(), wantH; got != want {
		t.Fatalf("local emulator height %d, want %d", got, want)
	}
}

// TestNewWindowOnNewWorkspacePTYSize is the user-facing shape of the same bug:
// switch to an empty workspace, create a window there, and the pane's shell must
// be the size of the pane.
func TestNewWindowOnNewWorkspacePTYSize(t *testing.T) {
	prevAnim := config.AnimationsEnabled
	config.AnimationsEnabled = false
	t.Cleanup(func() { config.AnimationsEnabled = prevAnim })

	const width, height = 130, 55

	// The client has already switched to the empty workspace 3.
	m := newTilingClient(width, height, 3)
	daemonState := &session.SessionState{
		Name:             "tiling",
		CurrentWorkspace: 3,
		AutoTiling:       true,
		WorkspaceFocus:   map[int]string{},
		Version:          1,
	}
	daemonNewWindow(daemonState, "win-00000000000000000000000000000002", width, height, 3)

	if err := m.ApplyStateSync(daemonState); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	win := m.Windows[0]

	var pty ptySpy
	pty.install(win)

	// Any later daemon-side mutation (focus, a PTY resize ack, a title change)
	// re-emits canonical state that still carries Unplaced.
	daemonState.Version++
	if err := m.ApplyStateSync(daemonState); err != nil {
		t.Fatalf("echo sync: %v", err)
	}

	if pty.w != win.ContentWidth() || pty.h != win.ContentHeight() {
		t.Fatalf("new-workspace pane: PTY %dx%d, pane %dx%d",
			pty.w, pty.h, win.ContentWidth(), win.ContentHeight())
	}
}

// TestSettledPaneAnnouncesNothing is the other side of the same rule, and the
// regression the announcement record was introduced for: once a pane's size has
// reached the shell, nothing that does not change that size may reach it again.
// Every announcement is a SIGWINCH, and a shell answers one by repainting its
// prompt over whatever is on screen.
func TestSettledPaneAnnouncesNothing(t *testing.T) {
	prevAnim := config.AnimationsEnabled
	config.AnimationsEnabled = false
	t.Cleanup(func() { config.AnimationsEnabled = prevAnim })

	const width, height = 130, 55

	m := newTilingClient(width, height, 1)
	daemonState := &session.SessionState{
		Name:             "tiling",
		CurrentWorkspace: 1,
		AutoTiling:       true,
		WorkspaceFocus:   map[int]string{},
		Version:          1,
	}
	daemonNewWindow(daemonState, "win-00000000000000000000000000000004", width, height, 1)
	if err := m.ApplyStateSync(daemonState); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	win := m.Windows[0]

	// The window has settled: the client's own geometry is what the daemon holds,
	// and the PTY carries it.
	settled := m.BuildSessionState()
	var pty ptySpy
	pty.install(win)

	// Ordinary traffic at the settled size: repeated syncs, a workspace switch
	// away and back, and the PTY-dimension sync a session switch runs.
	for range 3 {
		settled.Version++
		if err := m.ApplyStateSync(settled); err != nil {
			t.Fatalf("settled sync: %v", err)
		}
	}
	m.SwitchToWorkspace(5)
	m.SwitchToWorkspace(1)
	m.SyncDaemonPTYDimensions()

	if pty.calls != 0 {
		t.Fatalf("a settled pane was resized %d times (last %dx%d); every one is a SIGWINCH",
			pty.calls, pty.w, pty.h)
	}
}

// TestNewWindowOnNonCurrentWorkspaceSizedOnSwitch covers a window created on a
// workspace this client is not looking at: the tiler skips it, so it is sized by
// the retile that runs when the user switches to it. Its PTY must end up the
// size of the pane it is shown at.
func TestNewWindowOnNonCurrentWorkspaceSizedOnSwitch(t *testing.T) {
	prevAnim := config.AnimationsEnabled
	config.AnimationsEnabled = false
	t.Cleanup(func() { config.AnimationsEnabled = prevAnim })

	const width, height = 130, 55

	m := newTilingClient(width, height, 1)
	daemonState := &session.SessionState{
		Name:             "tiling",
		CurrentWorkspace: 1,
		AutoTiling:       true,
		WorkspaceFocus:   map[int]string{},
		Version:          1,
	}
	// Created on workspace 4 while this client is on workspace 1.
	daemonNewWindow(daemonState, "win-00000000000000000000000000000003", width, height, 4)

	if err := m.ApplyStateSync(daemonState); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	win := m.Windows[0]

	var pty ptySpy
	pty.install(win)

	m.SwitchToWorkspace(4)

	if pty.w != win.ContentWidth() || pty.h != win.ContentHeight() {
		t.Fatalf("pane shown after workspace switch: PTY %dx%d, pane %dx%d",
			pty.w, pty.h, win.ContentWidth(), win.ContentHeight())
	}
}
