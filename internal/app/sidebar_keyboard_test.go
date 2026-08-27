package app

import (
	"reflect"
	"strings"
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

// TestRailCursorNavigatesRows checks j/k walk the nav rows and g/G hit the ends.
func TestRailCursorNavigatesRows(t *testing.T) {
	m, tree := railOS(t)
	_ = tree
	start := navIndexOfSession(m, "main")
	if m.SidebarCursor != start {
		t.Fatalf("cursor started at %d, want the current session (%d)", m.SidebarCursor, start)
	}
	m.SidebarCursorMove(1)
	if m.SidebarCursor != start+1 {
		t.Fatalf("j moved cursor to %d, want %d", m.SidebarCursor, start+1)
	}
	m.SidebarCursorLast()
	last := len(m.SidebarNav) - 1
	if m.SidebarCursor != last {
		t.Fatalf("G moved cursor to %d, want last (%d)", m.SidebarCursor, last)
	}
	m.SidebarCursorMove(1) // clamped at the bottom
	if m.SidebarCursor != last {
		t.Fatalf("j past the end moved to %d, want clamped at %d", m.SidebarCursor, last)
	}
	m.SidebarCursorFirst()
	if m.SidebarCursor != 0 {
		t.Fatalf("g moved cursor to %d, want 0", m.SidebarCursor)
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

// TestRailJumpSelectsNthSession checks 1..9 land the cursor on the n-th session
// row, mirroring a click on it.
func TestRailJumpSelectsNthSession(t *testing.T) {
	m, tree := railOS(t)
	_ = tree
	m.SidebarJumpToSession(2)
	want := navIndexOfSession(m, "scratch") // second session in the fixture
	if m.SidebarCursor != want {
		t.Fatalf("jump 2 put cursor at %d, want scratch's row %d", m.SidebarCursor, want)
	}
	if m.SidebarNav[m.SidebarCursor].SessionID != "scratch" {
		t.Fatalf("jump 2 selected %q, want scratch", m.SidebarNav[m.SidebarCursor].SessionID)
	}
}

// TestRailDockPillReadsSidebar checks the dock mode pill announces rail focus.
func TestRailDockPillReadsSidebar(t *testing.T) {
	m, tree := railOS(t)
	_ = tree
	text, _, _, _, _ := m.buildDockLeftText()
	if !strings.Contains(text, "SIDEBAR") {
		t.Fatalf("dock left text = %q, want it to contain SIDEBAR while the rail is focused", text)
	}
	m.SidebarFocused = false
	text, _, _, _, _ = m.buildDockLeftText()
	if strings.Contains(text, "SIDEBAR") {
		t.Fatalf("dock still reads SIDEBAR after leaving the rail: %q", text)
	}
}
