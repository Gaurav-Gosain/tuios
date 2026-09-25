package app

import (
	"reflect"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// railOS returns a focused rail over the three-session fixture with its nav rows
// built, plus the tree so a test can re-render after a mutation.
func railOS(t *testing.T) (*OS, sessiontree.Tree) {
	t.Helper()
	m, tree := sidebarMultiSessionOS(t, 120, 40)
	m.SidebarFocused = true
	m.sidebarPanelLinesForTree(tree) // publish SidebarNav
	m.SidebarCursor = m.sidebarCurrentSessionNavIndex()
	return m, tree
}

// navIndexOfSession returns the nav index of a session row, or -1.
func navIndexOfSession(m *OS, id string) int {
	for i, r := range m.SidebarNav {
		if r.Kind == sidebarRowSession && r.SessionID == id {
			return i
		}
	}
	return -1
}

// TestRailEnterExitTogglesFocus checks entering reveals a hidden rail and lands
// the cursor on the current session, and exiting hides the rail it revealed.
func TestRailEnterExitTogglesFocus(t *testing.T) {
	m, tree := sidebarMultiSessionOS(t, 120, 40)
	// Start with the rail off so entering must reveal it.
	m.Settings.SidebarEnabled = false
	m.UserConfig = nil
	m.sidebarPanelLinesForTree(tree)

	m.EnterSidebarFocus()
	if !m.SidebarFocused {
		t.Fatal("EnterSidebarFocus did not take focus")
	}
	if !m.Settings.SidebarEnabled || !m.SidebarRevealedForFocus {
		t.Fatal("entering a hidden rail did not reveal it")
	}

	m.ExitSidebarFocus()
	if m.SidebarFocused {
		t.Fatal("ExitSidebarFocus did not release focus")
	}
	if m.Settings.SidebarEnabled {
		t.Fatal("exiting did not hide the rail it had revealed")
	}
}

// TestRailCursorExpandCollapseStepSections checks h/l (the old collapse and
// expand keys) walk the cursor between the rail's sections instead: a flat
// rail has nothing left in it to expand or collapse, so the keys were
// repurposed for the one thing left shaped like a depth to step through, and
// they stop at the ends rather than wrapping.
func TestRailCursorExpandCollapseStepSections(t *testing.T) {
	m, tree := railOS(t)
	_ = tree
	if row, ok := m.sidebarCursorRow(); !ok || row.Kind != sidebarRowSession {
		t.Fatalf("cursor did not start on a session row: %+v ok=%v", row, ok)
	}

	m.SidebarCursorExpand() // one section forward: sessions -> terminals
	if row, ok := m.sidebarCursorRow(); !ok || row.Kind != sidebarRowWindow {
		t.Fatalf("l moved the cursor to %+v, want the terminals section", row)
	}

	m.SidebarCursorCollapse() // one section back: terminals -> sessions
	if row, ok := m.sidebarCursorRow(); !ok || row.Kind != sidebarRowSession {
		t.Fatalf("h moved the cursor to %+v, want back to sessions", row)
	}

	m.SidebarCursorCollapse() // already the first section: nowhere left to go
	if row, ok := m.sidebarCursorRow(); !ok || row.Kind != sidebarRowSession {
		t.Fatalf("h past the first section moved to %+v, want to stay put", row)
	}
}

// TestRailActivateEqualsClick checks enter on a row is the same OS mutation a
// click on it is: on the current session it attaches (a no-op, since it is
// already attached) and keeps the rail, on a window it focuses that pane and
// asks to leave the rail.
func TestRailActivateEqualsClick(t *testing.T) {
	m, tree := railOS(t)
	_ = tree

	// Current session: enter attaches, like clicking its row, and stays on
	// the rail since navigating sessions is not a request for a pane.
	if exit := m.SidebarActivateCursor(); exit {
		t.Fatal("activating the current session asked to leave the rail")
	}

	// Window row: enter focuses the pane and asks to exit, exactly as a click.
	m.SidebarCursor = m.sidebarFirstRowOfKind(sidebarRowWindow) // first row of the terminals section
	row := m.SidebarNav[m.SidebarCursor]
	if row.Kind != sidebarRowWindow {
		t.Fatalf("nav[%d] is %v, want a window row", m.SidebarCursor, row.Kind)
	}
	m.FocusedWindow = 2
	exit := m.SidebarActivateCursor()
	if !exit {
		t.Fatal("activating a window row did not ask to leave the rail")
	}
	if got := m.Windows[m.FocusedWindow].ID; got != row.WindowID {
		t.Fatalf("enter focused %q, want %q", got, row.WindowID)
	}
}

// TestRailReorderMatchesDrag checks J/K reorder the cursor session and persist,
// landing the same SidebarOrder a drag would.
func TestRailReorderMatchesDrag(t *testing.T) {
	m, tree := railOS(t)
	_ = tree
	// Cursor on main; J moves it down past scratch.
	m.SidebarReorderCursor(1)
	if want := []string{"scratch", "main", "deploy"}; !reflect.DeepEqual(m.SidebarOrder, want) {
		t.Fatalf("J reorder = %v, want %v", m.SidebarOrder, want)
	}
	// The moved session is persisted.
	fresh := &OS{Settings: config.Global}
	fresh.loadSidebarState()
	if want := []string{"scratch", "main", "deploy"}; !reflect.DeepEqual(fresh.SidebarOrder, want) {
		t.Fatalf("fresh OS loaded order %v, want %v", fresh.SidebarOrder, want)
	}
	// The cursor follows the session to its new slot after the relayout.
	m.sidebarPanelLinesForTree(tree)
	if got := navIndexOfSession(m, "main"); m.SidebarCursor != got {
		t.Fatalf("cursor at %d after reorder, want main's new row %d", m.SidebarCursor, got)
	}
}
