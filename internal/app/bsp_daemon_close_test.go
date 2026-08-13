package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestDaemonCloseKeepsTheOtherPanesPut is the daemon half of the reported case.
// Closing a pane is a local edit to one corner of the layout: the sibling takes
// the freed space and nothing else moves. The panes on the far side of the tree
// have nothing to do with the pane that went away, so if they move, the layout
// the user built is gone.
func TestDaemonCloseKeepsTheOtherPanesPut(t *testing.T) {
	down, right := layout.PreselectionDown, layout.PreselectionRight

	m := splitOS(t)
	m.IsDaemonSession = true
	split(m, "window-0001", right)
	split(m, "window-0002", down)
	split(m, "window-0003", right)
	skewTreeRatios(m.WorkspaceTrees[m.CurrentWorkspace], 0.25)
	m.ApplyBSPLayout()

	bounds := m.GetBSPBounds()
	before := map[string]layout.Rect{}
	for id, r := range m.WorkspaceTrees[m.CurrentWorkspace].ApplyLayout(bounds, m.separatorGap()) {
		before[m.getWindowByIntID(id).ID] = r
	}

	// The daemon closes window-0004 and pushes the survivors.
	const gone = "window-0004"
	state := &session.SessionState{
		Name:             "test-session",
		CurrentWorkspace: 1,
		AutoTiling:       true,
		FocusedWindowID:  "window-0001",
		WorkspaceFocus:   map[int]string{},
		Version:          99,
	}
	for _, w := range m.Windows {
		if w.ID == gone {
			continue
		}
		state.Windows = append(state.Windows, session.WindowState{
			ID: w.ID, PTYID: "pty-" + w.ID, Workspace: 1, X: w.X, Y: w.Y, Width: w.Width, Height: w.Height,
		})
	}
	if err := m.ApplyStateSync(state); err != nil {
		t.Fatalf("ApplyStateSync failed: %v", err)
	}

	checkWorkspaceLayout(t, m, "after a daemon close")

	after := map[string]layout.Rect{}
	for id, r := range m.WorkspaceTrees[m.CurrentWorkspace].ApplyLayout(bounds, m.separatorGap()) {
		after[m.getWindowByIntID(id).ID] = r
	}

	// window-0004 split window-0003, so window-0003 inherits their shared box and
	// is allowed to grow. Nothing else has any reason to move.
	for _, id := range []string{"window-0001", "window-0002"} {
		if after[id] != before[id] {
			t.Errorf("closing %s moved %s from %+v to %+v", gone, id, before[id], after[id])
		}
	}
	if want := union(before["window-0003"], before[gone]); after["window-0003"] != want {
		t.Errorf("window-0003 = %+v, want %+v (its own box plus the closed pane's)",
			after["window-0003"], want)
	}
}

func union(a, b layout.Rect) layout.Rect {
	x, y := min(a.X, b.X), min(a.Y, b.Y)
	right, bottom := max(a.X+a.W, b.X+b.W), max(a.Y+a.H, b.Y+b.H)
	return layout.Rect{X: x, Y: y, W: right - x, H: bottom - y}
}
