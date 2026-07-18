package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// syncWindow builds a window state the daemon would push, without a PTY.
func syncWindow(id string) session.WindowState {
	return session.WindowState{
		ID: id, PTYID: "pty-" + id, Title: id,
		X: 0, Y: 0, Width: 40, Height: 12, Workspace: 1,
	}
}

// osWithWindow builds a model holding one window that a sync can then remove.
// It deliberately does not go through AddWindow: in a daemon session that sends
// an intent and creates nothing locally, which is the behavior under test.
func osWithWindow(t *testing.T, id string) (*OS, *terminal.Window) {
	t.Helper()
	m := NewOS(OSOptions{})
	m.Width, m.Height = 100, 40
	m.CurrentWorkspace = 1

	w := terminal.NewDaemonWindow(id, id, 0, 0, 40, 12, 0, "pty-"+id, nil)
	if w == nil {
		t.Fatal("NewDaemonWindow returned nil")
	}
	w.Workspace = 1
	m.Windows = []*terminal.Window{w}
	m.FocusedWindow = 0
	return m, w
}

// TestSyncCloseReleasesWindowReferences pins the teardown a close now runs.
// Closing is the daemon's, so this is the only teardown a daemon session
// performs, including for a close the user asked for. A reference left behind
// here is a window that keeps animating after it is gone, or a BSP id handed to
// two windows at once.
func TestSyncCloseReleasesWindowReferences(t *testing.T) {
	m, w := osWithWindow(t, "doomed")

	// Give the window everything a live one accumulates.
	intID := m.getWindowIntID(w.ID)
	if intID == 0 {
		t.Fatal("window was not assigned a BSP id")
	}
	m.Animations = []*ui.Animation{{Window: w}}
	m.MultifocusSet = map[string]bool{w.ID: true}

	// The daemon says the window is gone.
	if err := m.ApplyStateSync(&session.SessionState{
		Name: "s", CurrentWorkspace: 1, Windows: nil,
	}); err != nil {
		t.Fatalf("ApplyStateSync: %v", err)
	}

	if len(m.Windows) != 0 {
		t.Fatalf("window survived the close: %d remain", len(m.Windows))
	}
	for _, anim := range m.Animations {
		if anim.Window == w {
			t.Error("an animation still holds the closed window, which keeps it alive and animating")
		}
	}
	if _, ok := m.WindowToBSPID[w.ID]; ok {
		t.Error("the closed window still holds a BSP id mapping")
	}
	if _, ok := m.BSPIDToWindowID[intID]; ok {
		t.Error("the closed window's BSP int id was not released, so a later window can be handed it")
	}
	if m.MultifocusSet[w.ID] {
		t.Error("the closed window is still in the multifocus set")
	}
}

// TestSyncCloseOfLastWindowLeavesTerminalMode covers the one piece of the local
// close that was not about the window itself: with nothing focused, terminal
// mode has no terminal to send keys to.
func TestSyncCloseOfLastWindowLeavesTerminalMode(t *testing.T) {
	m, _ := osWithWindow(t, "only")
	m.Mode = TerminalMode

	if err := m.ApplyStateSync(&session.SessionState{
		Name: "s", CurrentWorkspace: 1, Windows: nil,
	}); err != nil {
		t.Fatalf("ApplyStateSync: %v", err)
	}

	if m.FocusedWindow != -1 {
		t.Errorf("FocusedWindow = %d, want -1", m.FocusedWindow)
	}
	if m.Mode != WindowManagementMode {
		t.Error("closing the last window left the client in terminal mode with nothing to type into")
	}
}

// TestSyncPlacesUnplacedWindow pins the other half of the create handshake: the
// daemon hands over a window it could not position, and the client positions it
// with the same rule it uses for a window it was asked for directly.
func TestSyncPlacesUnplacedWindow(t *testing.T) {
	m := NewOS(OSOptions{})
	m.Width, m.Height = 100, 40
	m.CurrentWorkspace = 1
	m.AutoTiling = false

	ws := syncWindow("fresh")
	ws.Unplaced = true
	// The nominal box the daemon sends is the whole session, which is exactly
	// what must not end up on screen.
	ws.X, ws.Y, ws.Width, ws.Height = 0, 0, 100, 40

	if err := m.ApplyStateSync(&session.SessionState{
		Name: "s", CurrentWorkspace: 1, Windows: []session.WindowState{ws},
	}); err != nil {
		t.Fatalf("ApplyStateSync: %v", err)
	}
	if len(m.Windows) != 1 {
		t.Fatalf("got %d windows, want 1", len(m.Windows))
	}

	wantX, wantY, wantW, wantH := m.NewWindowPlacement()
	got := m.Windows[0]
	if got.X != wantX || got.Y != wantY || got.Width != wantW || got.Height != wantH {
		t.Errorf("placed at %d,%d %dx%d, want this client's own placement %d,%d %dx%d",
			got.X, got.Y, got.Width, got.Height, wantX, wantY, wantW, wantH)
	}
}

// TestSyncLeavesPlacedWindowAlone is the guard on the flag's default: a window
// without it carries geometry someone chose, and re-placing it would move
// windows around on every push.
func TestSyncLeavesPlacedWindowAlone(t *testing.T) {
	m := NewOS(OSOptions{})
	m.Width, m.Height = 100, 40
	m.CurrentWorkspace = 1
	m.AutoTiling = false

	ws := syncWindow("settled")
	ws.X, ws.Y, ws.Width, ws.Height = 7, 3, 33, 11

	if err := m.ApplyStateSync(&session.SessionState{
		Name: "s", CurrentWorkspace: 1, Windows: []session.WindowState{ws},
	}); err != nil {
		t.Fatalf("ApplyStateSync: %v", err)
	}
	got := m.Windows[0]
	if got.X != 7 || got.Y != 3 || got.Width != 33 || got.Height != 11 {
		t.Errorf("geometry = %d,%d %dx%d, want the synced 7,3 33x11",
			got.X, got.Y, got.Width, got.Height)
	}
}
