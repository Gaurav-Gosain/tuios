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

// TestRailActivateKeepsThePaneItWasAskedFor is the other side of the rule:
// enter on a terminal row is a request for that pane, so leaving the rail must
// not pull focus back off it.
func TestRailActivateKeepsThePaneItWasAskedFor(t *testing.T) {
	m, tree := sidebarMultiSessionOS(t, 120, 40)
	m.sidebarPanelLinesForTree(tree)
	m.FocusedWindow = 0
	m.Mode = TerminalMode

	m.EnterSidebarFocus()
	target := navIndexOfWindow(m, "cccccccc3333")
	if target < 0 {
		t.Fatal("the fixture has no terminal row for logs")
	}
	m.sidebarSetCursor(target)

	if !m.SidebarActivateCursor() {
		t.Fatal("activating a terminal row did not ask to leave the rail")
	}
	m.ExitSidebarFocus()

	if got := m.GetFocusedWindow(); got == nil || got.ID != "cccccccc3333" {
		t.Errorf("focused %v, want the pane the rail was asked for", got)
	}
	if m.Mode != TerminalMode {
		t.Errorf("mode = %v, want terminal", m.Mode)
	}
}
