package app

import (
	"strconv"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestRevealOffsetIsTheLeastScroll pins the arithmetic: a row above the fold
// lands on the first line, a row below it on the last drawn line, and a row
// already on screen moves nothing.
func TestRevealOffsetIsTheLeastScroll(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		scroll, idx, rows, total int
		want                     int
	}{
		{"fits, no move", 0, 3, 5, 5, 0},
		{"already visible", 2, 4, 5, 10, 2},
		{"above the fold", 4, 1, 5, 10, 1},
		{"below the fold lands above the +N line", 0, 4, 5, 10, 1},
		{"last row lands at the end", 0, 9, 5, 10, 5},
		{"the row under the +N line is not visible", 0, 4, 5, 10, 1},
		{"at the end every line is a row", 5, 9, 5, 10, 5},
		{"no rows at all", 0, 0, 0, 10, 0},
	} {
		if got := sidebarRevealOffset(tc.scroll, tc.idx, tc.rows, tc.total); got != tc.want {
			t.Errorf("%s: sidebarRevealOffset(%d, %d, %d, %d) = %d, want %d",
				tc.name, tc.scroll, tc.idx, tc.rows, tc.total, got, tc.want)
		}
	}
}

// tallTerminalsOS is a short rail over many panes, so the terminals section
// has rows below its fold.
func tallTerminalsOS(t *testing.T, n int) (*OS, sessiontree.Tree) {
	t.Helper()
	m := newNarrowOS(t, 120, 16)
	m.CurrentWorkspace = 1
	m.SessionName = "main"
	wins := make([]sessiontree.WindowInput, 0, n)
	for i := range n {
		id := "win-" + strconv.Itoa(i)
		m.Windows = append(m.Windows, &terminal.Window{ID: id, CustomName: "pane" + strconv.Itoa(i), Width: 40, Height: 20, Workspace: 1})
		wins = append(wins, sessiontree.WindowInput{ID: id, Title: "pane" + strconv.Itoa(i), Workspace: 1, Focused: i == 0})
	}
	m.FocusedWindow = 0
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m.Settings = config.Global
	m.SidebarOrder = nil
	// More sessions than a short rail can list, whatever else it draws: the
	// reveal is what brings the last of them on screen, and a fixture where
	// the switch itself freed enough lines would pass without one.
	sessions := []sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, CurrentWorkspace: 1, Windows: wins},
	}
	for _, name := range []string{"api", "docs", "ops", "web", "cache", "queue", "auth", "bill", "cron", "edge", "logs", "mail", "db"} {
		sessions = append(sessions, sessiontree.SessionInput{Name: name})
	}
	tree := sessiontree.Build(sessions)
	return m, tree
}

// terminalRowOnScreen reports whether the terminals section drew a row for the
// pane.
func terminalRowOnScreen(m *OS, id string) bool {
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowWindow && h.WindowID == id {
			return true
		}
	}
	return false
}

// TestFocusingAPaneRevealsItsRow: a pane below the terminals fold comes on
// screen when it is focused, and only then; a frame with the same focus leaves
// the section where it is.
func TestFocusingAPaneRevealsItsRow(t *testing.T) {
	m, tree := tallTerminalsOS(t, 12)
	m.sidebarPanelLinesForTree(tree)
	last := "win-11"
	if terminalRowOnScreen(m, last) {
		t.Fatal("the fixture's last pane is already on screen; the rail is not short enough to test a reveal")
	}
	before := m.SidebarScrollT

	m.FocusedWindow = 11
	m.sidebarPanelLinesForTree(tree)
	if !terminalRowOnScreen(m, last) {
		t.Fatalf("focusing the last pane left its row off screen (scroll %d -> %d)", before, m.SidebarScrollT)
	}
	if m.SidebarScrollT == 0 {
		t.Fatal("the terminals section did not scroll")
	}

	// The same focus a frame later: nothing moves, even after the reader
	// wheels away.
	settled := m.SidebarScrollT
	m.sidebarPanelLinesForTree(tree)
	if m.SidebarScrollT != settled {
		t.Fatalf("a frame with no focus change moved the section from %d to %d", settled, m.SidebarScrollT)
	}
	m.SidebarScrollT = 0
	m.sidebarPanelLinesForTree(tree)
	if m.SidebarScrollT != 0 {
		t.Fatalf("the reveal fought the wheel: scroll went back to %d", m.SidebarScrollT)
	}
}

// TestAWheelBetweenFramesOutranksTheReveal: a focus change and a wheel in the
// same gap between frames is the reader saying where to look, and the reveal
// stands down.
func TestAWheelBetweenFramesOutranksTheReveal(t *testing.T) {
	m, tree := tallTerminalsOS(t, 12)
	m.sidebarPanelLinesForTree(tree)
	m.FocusedWindow = 11
	m.SidebarScrollT = 2 // the wheel moved it since the last frame
	m.sidebarPanelLinesForTree(tree)
	if m.SidebarScrollT != 2 {
		t.Fatalf("the reveal overrode a wheel that came first: scroll = %d, want 2", m.SidebarScrollT)
	}
}

// TestSwitchingASessionRevealsItsRow: the sessions section scrolls to the
// attached session when the attachment changes.
func TestSwitchingASessionRevealsItsRow(t *testing.T) {
	m, tree := tallTerminalsOS(t, 3)
	// A rail short enough that the six sessions do not all fit.
	m.Height = 12
	m.sidebarPanelLinesForTree(tree)
	onScreen := func(id string) bool {
		for _, h := range m.SidebarHits {
			if h.Kind == sidebarRowSession && h.SessionID == id {
				return true
			}
		}
		return false
	}
	if onScreen("db") {
		t.Fatal("the last session is already on screen; the rail is not short enough to test a reveal")
	}
	m.SessionName = "db"
	for i := range tree.Sessions {
		tree.Sessions[i].IsCurrent = tree.Sessions[i].ID == "db"
	}
	m.sidebarPanelLinesForTree(tree)
	if !onScreen("db") {
		t.Fatalf("switching to the last session left its row off screen (scroll %d)", m.SidebarScrollS)
	}
}
