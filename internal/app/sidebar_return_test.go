package app

import "testing"

// TestRailReturnsThePaneItBorrowed pins the browse case: walking the rail and
// pressing esc puts the user back on the pane they were typing in.
func TestRailReturnsThePaneItBorrowed(t *testing.T) {
	m, tree := sidebarMultiSessionOS(t, 120, 40)
	m.sidebarPanelLinesForTree(tree)
	m.FocusedWindow = 2 // "logs"
	m.Mode = TerminalMode

	m.EnterSidebarFocus()
	m.SidebarCursorMove(1) // browse, which is what esc undoes
	m.FocusWindow(0)

	m.ExitSidebarFocus()

	if m.Mode != TerminalMode {
		t.Errorf("mode = %v, want terminal", m.Mode)
	}
	if got := m.GetFocusedWindow(); got == nil || got.ID != "cccccccc3333" {
		t.Errorf("focused %v, want the pane the rail was entered from", got)
	}
}
