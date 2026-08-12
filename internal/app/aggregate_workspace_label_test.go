package app

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestAggregateViewNamesTheWorkspaceTheWindowsAreIn is the regression test for
// the aggregate view labelling every group one workspace too high.
//
// Workspaces are 1-based throughout: occupiedWorkspaces counts from 1, the dock
// chip prints the number as it stands, and a window's Workspace field is that
// same number. The stored value was right and the label added one to it, so the
// panel showed workspace 1's windows under "Workspace 2" and offered no
// "Workspace 1" at all.
func TestAggregateViewNamesTheWorkspaceTheWindowsAreIn(t *testing.T) {
	m := &OS{
		WorkspaceFocus:   map[int]int{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            140,
		Height:           44,
	}
	for _, ws := range []int{1, 3} {
		win := newTestWindow(t, fmt.Sprintf("agg-ws%d", ws), 40, 12)
		win.Workspace = ws
		win.SetTitle(fmt.Sprintf("pane-in-%d", ws))
		m.Windows = append(m.Windows, win)
	}
	m.ShowAggregateView = true

	frame := ansi.Strip(lipgloss.Sprint(m.GetCanvas(true).Render()))

	for _, ws := range []int{1, 3} {
		if !strings.Contains(frame, fmt.Sprintf("Workspace %d:", ws)) {
			t.Errorf("no group labelled %q; the panel names a workspace its windows are not in", fmt.Sprintf("Workspace %d:", ws))
		}
	}
	for _, ws := range []int{2, 4} {
		if strings.Contains(frame, fmt.Sprintf("Workspace %d:", ws)) {
			t.Errorf("panel shows a group for empty workspace %d", ws)
		}
	}
}

// TestAggregateFilterMatchesTheWorkspaceNumber pins the other half of the same
// off-by-one: the search text a query is matched against carried the shifted
// number too, so filtering by a workspace found the wrong one's windows.
func TestAggregateFilterMatchesTheWorkspaceNumber(t *testing.T) {
	items := []AggregateViewItem{
		{Window: &terminal.Window{}, Workspace: 1, Title: "alpha"},
		{Window: &terminal.Window{}, Workspace: 2, Title: "bravo"},
	}
	got := FilterAggregateViewItems(items, "2")
	if len(got) != 1 || got[0].Workspace != 2 {
		t.Fatalf("query %q matched %+v, want only the workspace 2 window", "2", got)
	}
}
