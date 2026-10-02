package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// lastPaneTestOS is three panes on one workspace, focus on the first.
func lastPaneTestOS(t *testing.T) *OS {
	t.Helper()
	m := newNarrowOS(t, 120, 40)
	m.SessionName = "main"
	m.WorkspaceFocus = map[int]int{}
	m.Windows = []*terminal.Window{
		{ID: "one", CustomName: "one", Width: 40, Height: 20, Workspace: 1},
		{ID: "two", CustomName: "two", Width: 40, Height: 20, Workspace: 1},
		{ID: "three", CustomName: "three", Width: 40, Height: 20, Workspace: 1},
	}
	m.FocusedWindow = 0
	return m
}

// TestLastPaneAlternates checks the flip invariant: alternating presses walk
// between the last two panes, whatever the route focus took between them.
func TestLastPaneAlternates(t *testing.T) {
	m := lastPaneTestOS(t)

	if m.LastPane() {
		t.Fatal("nothing was focused before, yet LastPane claimed a target")
	}
	m.FocusWindow(1)
	m.FocusWindow(2)
	if !m.LastPane() || m.FocusedWindow != 1 {
		t.Fatalf("first flip landed on %d, want 1", m.FocusedWindow)
	}
	if !m.LastPane() || m.FocusedWindow != 2 {
		t.Fatalf("second flip landed on %d, want 2", m.FocusedWindow)
	}
}

// TestLastPaneAcrossWorkspaces checks a flip that has to cross a workspace
// switch lands on the right pane, since every jump funnels through
// FocusWindow's workspace switch.
func TestLastPaneAcrossWorkspaces(t *testing.T) {
	m := lastPaneTestOS(t)
	m.Windows[2].Workspace = 3

	m.FocusWindow(2)
	if m.CurrentWorkspace != 3 {
		t.Fatalf("workspace = %d, want 3 after the jump", m.CurrentWorkspace)
	}
	if !m.LastPane() || m.FocusedWindow != 0 || m.CurrentWorkspace != 1 {
		t.Fatalf("flip back landed on pane %d workspace %d, want pane 0 workspace 1",
			m.FocusedWindow, m.CurrentWorkspace)
	}
}

// TestLastPaneDeadTarget checks a closed previous pane reports no move instead
// of landing on whatever pane reused the index.
func TestLastPaneDeadTarget(t *testing.T) {
	m := lastPaneTestOS(t)
	m.FocusWindow(1)
	m.FocusWindow(2)                             // previous is now pane two
	m.Windows = []*terminal.Window{m.Windows[0]} // two and three closed
	m.FocusedWindow = 0

	if m.LastPane() {
		t.Fatal("a closed target claimed to move")
	}
	if m.FocusedWindow != 0 {
		t.Fatalf("focus moved to %d on a dead target", m.FocusedWindow)
	}
}

// TestTuiosLinkTarget checks the two URI shapes the wait bar links with, and
