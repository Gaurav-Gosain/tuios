package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// These tests cover what happens when the window set changes underneath the
// renderer: the daemon owns window lifecycle but not layout, so a window it
// created arrives through a state sync with nominal geometry and no place in any
// tiling structure. Absorbing that is what makes the TUI a subscriber to the
// daemon rather than the only thing allowed to create windows.

// tiledOS builds an auto-tiling model holding one window, sized so tiling has
// room to work.
func tiledOS(existingID string) *OS {
	m := &OS{
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Width:            120,
		Height:           40,
		AutoTiling:       true,
		UseBSPLayout:     true,
		Windows: []*terminal.Window{
			{ID: existingID, Workspace: 1, Width: 120, Height: 40},
		},
		FocusedWindow: 0,
	}
	m.TileAllWindows()
	return m
}

// syncWith returns the state the daemon would push after creating daemonID
// alongside existingID. Daemon-created windows carry a nominal full-size box:
// the daemon has no viewport of its own to tile against.
func syncWith(existingID, daemonID string, width, height int) *session.SessionState {
	return &session.SessionState{
		Name:             "test-session",
		CurrentWorkspace: 1,
		AutoTiling:       true,
		FocusedWindowID:  daemonID,
		WorkspaceFocus:   map[int]string{},
		Windows: []session.WindowState{
			{ID: existingID, PTYID: "pty-1", Workspace: 1, Width: width, Height: height},
			{ID: daemonID, PTYID: "pty-2", Workspace: 1, Width: width, Height: height},
		},
		Version: 2,
	}
}

// TestSyncedWindowJoinsTheTiledLayout is the behavior a headless new-window has
// to produce in an attached TUI: the window does not just exist in the model, it
// takes its share of the screen. Before this, a daemon-created window kept the
// full-size geometry the daemon gave it and covered the windows already there.
func TestSyncedWindowJoinsTheTiledLayout(t *testing.T) {
	const existingID = "win-0000-0000-0000-0000-000000000001"
	const daemonID = "win-0000-0000-0000-0000-000000000002"

	m := tiledOS(existingID)
	if err := m.ApplyStateSync(syncWith(existingID, daemonID, 120, 40)); err != nil {
		t.Fatalf("ApplyStateSync failed: %v", err)
	}

	if len(m.Windows) != 2 {
		t.Fatalf("window count = %d, want 2", len(m.Windows))
	}

	tree := m.WorkspaceTrees[1]
	if tree == nil {
		t.Fatal("no BSP tree for workspace 1")
	}
	if intID := m.getWindowIntID(daemonID); !tree.HasWindow(intID) {
		t.Fatalf("daemon-created window (int ID %d) is not in the BSP tree; tree holds %v",
			intID, tree.GetAllWindowIDs())
	}

	// Both windows must have been given a share of the screen rather than the
	// full-size box the daemon sent. BSP applies its layout through snap
	// animations, so the target rects are what the tree resolved to.
	rects := tree.ApplyLayout(m.GetBSPBounds())
	if len(rects) != 2 {
		t.Fatalf("tree laid out %d windows, want 2", len(rects))
	}
	for intID, r := range rects {
		if r.W >= 120 {
			t.Errorf("window %d still spans the full width (%d): the sync was not tiled", intID, r.W)
		}
	}
}

// TestSyncedWindowJoinsTheScrollingLayout covers the layout with no self-repair.
// The BSP path inserts unknown windows on its own; the scrolling layout would
// silently leave the new window out of its columns, so it renders wherever the
// daemon happened to put it.
func TestSyncedWindowJoinsTheScrollingLayout(t *testing.T) {
	const existingID = "win-0000-0000-0000-0000-000000000001"
	const daemonID = "win-0000-0000-0000-0000-000000000002"

	m := tiledOS(existingID)
	m.UseBSPLayout = false
	m.UseScrollingLayout = true
	m.TileAllWindows()

	if err := m.ApplyStateSync(syncWith(existingID, daemonID, 120, 40)); err != nil {
		t.Fatalf("ApplyStateSync failed: %v", err)
	}

	sl := m.GetOrCreateScrollingLayout()
	if intID := m.getWindowIntID(daemonID); !sl.HasWindow(intID) {
		t.Fatalf("daemon-created window (int ID %d) has no column in the scrolling layout", intID)
	}
	if got := sl.WindowCount(); got != 2 {
		t.Errorf("scrolling layout holds %d windows, want 2", got)
	}
}

// TestSyncedCloseLeavesNoTile pins the other direction: a window closed on the
// daemon must give its space back, not leave a hole where its tile was.
func TestSyncedCloseLeavesNoTile(t *testing.T) {
	const keepID = "win-0000-0000-0000-0000-000000000001"
	const goneID = "win-0000-0000-0000-0000-000000000002"

	m := tiledOS(keepID)
	m.Windows = append(m.Windows, &terminal.Window{ID: goneID, Workspace: 1, Width: 60, Height: 40})
	m.TileAllWindows()
	if !m.WorkspaceTrees[1].HasWindow(m.getWindowIntID(goneID)) {
		t.Fatal("setup: second window never made it into the tree")
	}

	state := &session.SessionState{
		Name:             "test-session",
		CurrentWorkspace: 1,
		AutoTiling:       true,
		FocusedWindowID:  keepID,
		WorkspaceFocus:   map[int]string{},
		Windows: []session.WindowState{
			{ID: keepID, PTYID: "pty-1", Workspace: 1, Width: 60, Height: 40},
		},
		Version: 3,
	}
	if err := m.ApplyStateSync(state); err != nil {
		t.Fatalf("ApplyStateSync failed: %v", err)
	}

	if len(m.Windows) != 1 {
		t.Fatalf("window count = %d, want 1", len(m.Windows))
	}
	tree := m.WorkspaceTrees[1]
	if tree == nil {
		t.Fatal("no BSP tree for workspace 1")
	}
	if tree.HasWindow(m.getWindowIntID(goneID)) {
		t.Error("closed window is still in the BSP tree")
	}
	// The survivor takes the whole area back.
	rects := tree.ApplyLayout(m.GetBSPBounds())
	if len(rects) != 1 {
		t.Fatalf("tree laid out %d windows, want 1", len(rects))
	}
	for _, r := range rects {
		if r.W < 120 {
			t.Errorf("remaining window width = %d, want the full render width", r.W)
		}
	}
}

// TestSyncWithoutLifecycleChangeDoesNotRetile keeps the retile targeted. A sync
// that only moves geometry (the common case: another client rendering) must not
// re-run tiling, or every peer render would fight the local layout.
func TestSyncWithoutLifecycleChangeDoesNotRetile(t *testing.T) {
	const existingID = "win-0000-0000-0000-0000-000000000001"

	m := tiledOS(existingID)
	state := &session.SessionState{
		Name:             "test-session",
		CurrentWorkspace: 1,
		AutoTiling:       true,
		FocusedWindowID:  existingID,
		WorkspaceFocus:   map[int]string{},
		Windows: []session.WindowState{
			{ID: existingID, PTYID: "pty-1", Workspace: 1, X: 5, Y: 3, Width: 40, Height: 20},
		},
		Version: 2,
	}
	if err := m.ApplyStateSync(state); err != nil {
		t.Fatalf("ApplyStateSync failed: %v", err)
	}

	w := m.Windows[0]
	if w.X != 5 || w.Y != 3 || w.Width != 40 || w.Height != 20 {
		t.Errorf("geometry = (%d,%d) %dx%d, want the synced (5,3) 40x20: the sync was retiled",
			w.X, w.Y, w.Width, w.Height)
	}
}
