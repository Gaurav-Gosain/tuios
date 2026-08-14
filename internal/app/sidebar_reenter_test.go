package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// Enter on a terminal row hands the keyboard to that pane, which is what it is
// for. Coming back landed the cursor on the attached session's row every time,
// so a user three rows into the terminals section had to walk down again after
// every visit. The rail already remembers the mode and the pane it borrowed;
// the row it was left on is the same bargain pointing the other way.

// bandedLine is the rail line the cursor's highlight is actually painted on, or
// -1. The cursor and hover share one band, so this is what the user sees.
func bandedLine(t *testing.T, lines []string) int {
	t.Helper()
	found := -1
	for i, l := range lines {
		cells := stripCells(l)
		painted := 0
		for _, c := range cells {
			if bgOf(c) != "" {
				painted++
			}
		}
		// A banded row is filled edge to edge; a row with a coloured glyph or two
		// is not the cursor.
		if painted > len(cells)/2 {
			if found >= 0 {
				t.Fatalf("two rail lines are banded (%d and %d)", found, i)
			}
			found = i
		}
	}
	return found
}

func TestRailComesBackToTheRowItWasLeftOn(t *testing.T) {
	m, tree := sidebarMultiSessionOS(t, 120, 40)
	m.sidebarPanelLinesForTree(tree)
	m.FocusedWindow = 0
	m.Mode = TerminalMode

	m.EnterSidebarFocus()
	m.sidebarPanelLinesForTree(tree)
	target := navIndexOfWindow(m, "cccccccc3333")
	if target < 0 {
		t.Fatal("the fixture drew no terminal row for logs")
	}
	m.sidebarSetCursor(target)
	want, _ := m.sidebarCursorRow()
	leftOn := bandedLine(t, mustRailLines(t, m, tree))
	if leftOn < 0 {
		t.Fatal("the cursor row is not banded in the frame at all")
	}

	// Enter: the pane is what was asked for, and the rail gives the keyboard up.
	if !m.SidebarActivateCursor() {
		t.Fatal("enter on a terminal row did not ask to leave the rail")
	}
	m.ExitSidebarFocus()
	if got := m.GetFocusedWindow(); got == nil || got.ID != "cccccccc3333" {
		t.Fatalf("focused %v, want the pane enter asked for", got)
	}

	// Back in: the cursor is on the row it left from, in the frame and not only
	// in the index.
	m.EnterSidebarFocus()
	got, ok := m.sidebarCursorRow()
	if !ok || !sidebarNavRowsEqual(got, want) {
		t.Errorf("cursor came back to %+v, want the row it left from %+v", got, want)
	}
	if back := bandedLine(t, mustRailLines(t, m, tree)); back != leftOn {
		t.Errorf("the band came back on line %d, want the line it left from (%d)", back, leftOn)
	}
}

// TestRailFallsBackWhenTheRowIsGone: the remembered row is a request, not a
// promise. A pane closed while the user was away has no row to come back to.
func TestRailFallsBackWhenTheRowIsGone(t *testing.T) {
	m, tree := sidebarMultiSessionOS(t, 120, 40)
	m.sidebarPanelLinesForTree(tree)

	m.EnterSidebarFocus()
	m.sidebarPanelLinesForTree(tree)
	m.sidebarSetCursor(navIndexOfWindow(m, "cccccccc3333"))
	m.ExitSidebarFocus()

	// The pane goes away, and so does its row.
	m.Windows = m.Windows[:2]
	gone := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "claude", AgentState: "working", Focused: true},
			{ID: "bbbbbbbb2222", Title: "tests", AgentState: "needs_input"},
		}},
		{Name: "scratch", WindowCount: 2},
		{Name: "deploy", WindowCount: 1},
	})
	m.sidebarPanelLinesForTree(gone)

	m.EnterSidebarFocus()
	row, ok := m.sidebarCursorRow()
	if !ok || row.Kind != sidebarRowSession || row.SessionID != "main" {
		t.Errorf("with the remembered row gone the cursor landed on %+v, want the attached session", row)
	}
}

// TestRailRemembersAcrossAnEscBrowse: esc is the other way out, and it leaves
// the rail on a row just as squarely.
func TestRailRemembersAcrossAnEscBrowse(t *testing.T) {
	m, tree := sidebarMultiSessionOS(t, 120, 40)
	m.sidebarPanelLinesForTree(tree)

	m.EnterSidebarFocus()
	m.sidebarPanelLinesForTree(tree)
	m.SidebarCursorMove(2)
	want, _ := m.sidebarCursorRow()

	m.ExitSidebarFocus() // esc
	m.sidebarPanelLinesForTree(tree)
	m.EnterSidebarFocus()

	got, _ := m.sidebarCursorRow()
	if !sidebarNavRowsEqual(got, want) {
		t.Errorf("cursor came back to %+v, want %+v", got, want)
	}
}

// mustRailLines renders the rail with the cursor showing.
func mustRailLines(t *testing.T, m *OS, tree sessiontree.Tree) []string {
	t.Helper()
	lines, _ := m.sidebarPanelLinesForTree(tree)
	return lines
}
