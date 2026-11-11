package app_test

import (
	"maps"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/apptest"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The tmux-style pane commands that move panes between workspaces. The
// same-workspace join and the workspace a pane reports are covered by
// TestMarkAndJoinPane and TestMoveWorkspaceAndSendPane in e2e/tui; these stay
// for what one workspace of panes there cannot show: a pane leaving one
// workspace's tree for another's, and the per-workspace state that travels
// with a moved workspace.

// paneBoxes is the pane frames on screen by title.
func paneBoxes(t *testing.T, f *apptest.Fixture) map[string]apptest.Box {
	t.Helper()
	whole, cut := f.PaneFrames()
	if len(cut) > 0 {
		t.Fatalf("pane frames cut by the screen: %v\n%s", cut, f.Text())
	}
	out := map[string]apptest.Box{}
	for _, b := range whole {
		out[b.Title] = b
	}
	return out
}

// paneWorkspace is the workspace the model puts a pane on, by title.
func paneWorkspace(t *testing.T, f *apptest.Fixture, title string) int {
	t.Helper()
	for _, w := range f.M.Windows {
		if w.Title() == title {
			return w.Workspace
		}
	}
	t.Fatalf("no pane titled %q", title)
	return 0
}

// TestJoinMarkedPaneBringsAPaneFromAnotherWorkspace: a pane marked on
// workspace 2 and joined below the focused pane on workspace 1 comes over,
// lands below it, and is drawn once: it is in workspace 1's tree only, and
// the mark is spent. (A pane left in workspace 2's tree as well cannot be
// seen: tiling drops a tree entry with no pane on its workspace.) Negative
// control: skip MoveWindowToWorkspace in JoinMarkedPane.
func TestJoinMarkedPaneBringsAPaneFromAnotherWorkspace(t *testing.T) {
	f := apptest.New(t, apptest.Workspaces(map[int]int{1: 3, 2: 1}))
	const moved = "pane 4"

	f.Action("switch_workspace_2")
	if _, ok := paneBoxes(t, f)[moved]; !ok {
		t.Fatalf("workspace 2 does not show %q:\n%s", moved, f.Text())
	}
	f.Action("mark_pane")
	f.Action("switch_workspace_1")
	target := f.M.GetFocusedWindow().Title()
	f.Action("join_marked_down")

	if ws := paneWorkspace(t, f, moved); ws != 1 {
		t.Fatalf("%s is on workspace %d, want 1", moved, ws)
	}
	boxes := paneBoxes(t, f)
	if len(boxes) != 4 {
		t.Errorf("workspace 1 shows %d panes, want 4: %v", len(boxes), slices.Sorted(maps.Keys(boxes)))
	}
	a, d := boxes[target], boxes[moved]
	if d.Y0 <= a.Y1 || d.X0 != a.X0 {
		t.Errorf("%s (rows %d-%d, column %d) is not below %s (rows %d-%d, column %d)",
			moved, d.Y0, d.Y1, d.X0, target, a.Y0, a.Y1, a.X0)
	}
	if f.M.MarkedPaneID != "" {
		t.Errorf("the mark %q outlived the join", f.M.MarkedPaneID)
	}

	f.Action("switch_workspace_2")
	if boxes := paneBoxes(t, f); len(boxes) != 0 {
		t.Errorf("workspace 2 still shows %v after its pane left", slices.Sorted(maps.Keys(boxes)))
	}
}

// TestMoveWorkspaceBySwapsAndFollows: moving workspace 1 right swaps it with
// workspace 2 and the view follows. Its panes, its name and its layout go to
// workspace 2, and workspace 2's come to 1. Moving the first workspace left
// changes nothing. A move retiles nothing, so the layout is checked after
// the host terminal is resized and back, which retiles from workspace 2's
// tree, with a split widened first, since a fresh tree would give back the
// default layout.
// Negative control: renumber the panes but not the trees in
// renumberLocalWorkspaces.
func TestMoveWorkspaceBySwapsAndFollows(t *testing.T) {
	f := apptest.New(t, apptest.Workspaces(map[int]int{1: 3, 2: 1}))
	f.Push(func(s *session.SessionState) { s.WorkspaceNames = map[int]string{1: "code", 2: "logs"} })
	if f.M.WorkspaceNames[1] != "code" {
		t.Fatalf("the pushed workspace names did not arrive: %v", f.M.WorkspaceNames)
	}
	start := paneBoxes(t, f)
	for range 4 {
		f.Action("resize_master_grow")
	}
	before := paneBoxes(t, f)
	if maps.Equal(before, start) {
		t.Fatal("widening the split changed no pane; the layout check would prove nothing")
	}

	f.Action("move_workspace_right")

	if f.M.CurrentWorkspace != 2 {
		t.Errorf("the view is on workspace %d, want 2", f.M.CurrentWorkspace)
	}
	for title, want := range map[string]int{"pane 1": 2, "pane 2": 2, "pane 3": 2, "pane 4": 1} {
		if got := paneWorkspace(t, f, title); got != want {
			t.Errorf("%s is on workspace %d, want %d", title, got, want)
		}
	}
	if f.M.WorkspaceNames[2] != "code" || f.M.WorkspaceNames[1] != "logs" {
		t.Errorf("names are %v, want the two swapped", f.M.WorkspaceNames)
	}
	f.Send(tea.WindowSizeMsg{Width: 100, Height: 40})
	f.Send(tea.WindowSizeMsg{Width: 120, Height: 40})
	if after := paneBoxes(t, f); !maps.Equal(after, before) {
		t.Errorf("workspace 1's layout did not move with its panes:\nbefore %v\nafter  %v", before, after)
	}

	// At the first workspace there is nothing to the left.
	f.Action("switch_workspace_1")
	only := paneBoxes(t, f)
	f.Action("move_workspace_left")
	if f.M.CurrentWorkspace != 1 || paneWorkspace(t, f, "pane 4") != 1 || !maps.Equal(paneBoxes(t, f), only) {
		t.Error("moving the first workspace left changed something")
	}
}
