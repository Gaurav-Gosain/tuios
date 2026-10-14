package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// prefixTreeOS is a BSP-tiled model with panes a, b, c on workspace 1 and d on
// workspace 2, a focused.
func prefixTreeOS(t *testing.T) *OS {
	t.Helper()
	m := tiledOS("a")
	m.Settings.Motion = config.MotionNone // place panes at once, not over frames
	// The maps NewOS makes and a move between workspaces saves into.
	m.WorkspaceLayouts = map[int][]WindowLayout{}
	m.WorkspaceMasterRatio = map[int]float64{}
	m.WorkspaceHasCustom = map[int]bool{}
	m.Windows = append(m.Windows,
		&terminal.Window{ID: "b", Workspace: 1, Width: 60, Height: 40},
		&terminal.Window{ID: "c", Workspace: 1, Width: 60, Height: 40},
		&terminal.Window{ID: "d", Workspace: 2, Width: 120, Height: 40},
	)
	m.TileAllWindows()
	m.FocusedWindow = 0
	return m
}

// treeRect is where the current workspace's tree puts a pane. The fixture's
// panes have no terminal, so their own Width and Height do not follow.
func treeRect(t *testing.T, m *OS, id string) layout.Rect {
	t.Helper()
	r, ok := m.WorkspaceTrees[m.CurrentWorkspace].ApplyLayout(m.GetBSPBounds(), m.separatorGap())[m.GetWindowIntID(id)]
	if !ok {
		t.Fatalf("the tree does not place %q", id)
	}
	return r
}

func windowByID(t *testing.T, m *OS, id string) *terminal.Window {
	t.Helper()
	for _, w := range m.Windows {
		if w.ID == id {
			return w
		}
	}
	t.Fatalf("no window %q", id)
	return nil
}

// TestJoinMarkedPaneBringsAPaneFromAnotherWorkspace is join-pane's main use:
// the marked pane lives on another workspace and comes over. The same-workspace
// join, the mark and the no-mark case are covered by TestMarkAndJoinPane in
// e2e/tui; this one stays because it moves a pane between two workspace trees,
// which one session with one workspace of panes there does not.
func TestJoinMarkedPaneBringsAPaneFromAnotherWorkspace(t *testing.T) {
	m := prefixTreeOS(t)
	// Workspace 2 has a tree of its own, with d in it.
	m.CurrentWorkspace = 2
	m.TileAllWindows()
	m.CurrentWorkspace = 1
	dInt := m.GetWindowIntID("d")
	if tree := m.WorkspaceTrees[2]; tree == nil || !tree.HasWindow(dInt) {
		t.Fatal("setup: workspace 2's tree does not hold d")
	}
	m.MarkedPaneID = "d"
	m.JoinMarkedPane(layout.PreselectionDown)

	if ws := windowByID(t, m, "d").Workspace; ws != 1 {
		t.Fatalf("d is on workspace %d, want 1", ws)
	}
	a, d := treeRect(t, m, "a"), treeRect(t, m, "d")
	if d.Y < a.Y+a.H || d.X != a.X {
		t.Errorf("d (y=%d) is not below a (y=%d h=%d)", d.Y, a.Y, a.H)
	}
	if got := m.WorkspaceTrees[1].WindowCount(); got != 4 {
		t.Errorf("the tree holds %d panes, want 4", got)
	}
	// A pane in two trees would be tiled twice.
	if tree := m.WorkspaceTrees[2]; tree != nil && tree.HasWindow(dInt) {
		t.Error("d is still in workspace 2's tree")
	}
	if m.MarkedPaneID != "" {
		t.Errorf("the mark %q outlived the join", m.MarkedPaneID)
	}
}

// TestMoveWorkspaceBySwapsAndFollows moves workspace 1 right. The e2e test
// (TestMoveWorkspaceAndSendPane) sees which workspace a pane reports; this one
// stays for the per-workspace state it cannot see, the names and the tree: its
// panes and name go to workspace 2, workspace 2's come to 1, and the view follows.
func TestMoveWorkspaceBySwapsAndFollows(t *testing.T) {
	m := prefixTreeOS(t)
	m.WorkspaceNames = map[int]string{1: "code", 2: "logs"}
	tree1 := m.WorkspaceTrees[1]

	m.MoveWorkspaceBy(1)

	if m.CurrentWorkspace != 2 {
		t.Errorf("current workspace is %d, want 2", m.CurrentWorkspace)
	}
	for id, want := range map[string]int{"a": 2, "b": 2, "c": 2, "d": 1} {
		if got := windowByID(t, m, id).Workspace; got != want {
			t.Errorf("%s is on workspace %d, want %d", id, got, want)
		}
	}
	if m.WorkspaceNames[2] != "code" || m.WorkspaceNames[1] != "logs" {
		t.Errorf("names are %v, want the two swapped", m.WorkspaceNames)
	}
	if m.WorkspaceTrees[2] != tree1 {
		t.Error("workspace 1's tree did not move with its panes")
	}

	// At the first workspace there is nothing to the left.
	m.CurrentWorkspace = 1
	m.MoveWorkspaceBy(-1)
	if m.CurrentWorkspace != 1 || windowByID(t, m, "d").Workspace != 1 {
		t.Error("moving the first workspace left changed something")
	}
}
