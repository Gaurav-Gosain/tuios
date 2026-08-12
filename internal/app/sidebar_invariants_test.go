package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// Three sections with a filter, a sort, a peek and two rail states make the
// rail's two addressing lists far easier to pull apart than the tree did. These
// are the invariants that hold across every combination of them, not just the
// ones a feature's own test happened to render.

// TestRailAddressingHoldsAcrossEveryCombination walks the product of position,
// collapse, peek, filter, sort and height, and asserts the three things that
// have to be true of every frame: hits and nav name the same targets in the
// same order, no rectangle escapes the band or overlaps its predecessor, and
// the cursor lands on a row that exists.
func TestRailAddressingHoldsAcrossEveryCombination(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		for _, collapsed := range []bool{false, true} {
			for _, peek := range []string{"", "api", "gone"} {
				for _, filter := range []string{sidebarAgentsAll, sidebarAgentsSession} {
					for _, sortBy := range []string{sidebarAgentsPriority, sidebarAgentsRecent} {
						for _, h := range []int{30, 14, 9} {
							name := fmt.Sprintf("%s/collapsed=%v/peek=%q/%s/%s/h=%d", pos, collapsed, peek, filter, sortBy, h)
							t.Run(name, func(t *testing.T) {
								m, tree := sectionsTestOS(t, 120, h)
								withSidebar(t, true, pos, config.SidebarDefaultWidth)
								m.SidebarCollapsed = collapsed
								m.SidebarPeek = peek
								m.SidebarAgentFilter, m.SidebarAgentSort = filter, sortBy
								m.SidebarFocused = true
								m.sidebarPanelLinesForTree(tree)

								assertHitsFollowNav(t, m)
								assertHitsStayInTheBand(t, m)
								assertCursorIsOnARealRow(t, m)
							})
						}
					}
				}
			}
		}
	}
}

// assertHitsFollowNav is the index-for-index rule, stated as the subsequence it
// actually is: nav also carries the rows scrolled out of sight, which is what
// lets the keyboard reach them.
func assertHitsFollowNav(t *testing.T, m *OS) {
	t.Helper()
	j := 0
	for i, hit := range m.SidebarHits {
		want := navRowOf(hit)
		for j < len(m.SidebarNav) && !sidebarNavRowsEqual(m.SidebarNav[j], want) {
			j++
		}
		if j >= len(m.SidebarNav) {
			t.Fatalf("hit %d %+v has no nav row after the ones already matched", i, want)
		}
		j++
	}
}

// assertHitsStayInTheBand: a rectangle outside the rail routes a click on a
// pane to the rail, and one overlapping its predecessor makes whichever came
// first unreachable.
func assertHitsStayInTheBand(t *testing.T, m *OS) {
	t.Helper()
	w := m.GetSidebarWidth()
	x0 := 0
	if config.SidebarPosition == "right" {
		x0 = m.GetRenderWidth() - w
	}
	top, bottom := m.GetTopMargin(), m.GetTopMargin()+m.GetUsableHeight()

	for i, h := range m.SidebarHits {
		if h.X0 < x0 || h.X1 > x0+w || h.X0 >= h.X1 {
			t.Errorf("hit %d spans [%d,%d), outside the band [%d,%d)", i, h.X0, h.X1, x0, x0+w)
		}
		if h.Y0 < top || h.Y1 > bottom {
			t.Errorf("hit %d spans rows [%d,%d), outside the band [%d,%d)", i, h.Y0, h.Y1, top, bottom)
		}
		if i == 0 {
			continue
		}
		prev := m.SidebarHits[i-1]
		switch {
		case h.Y0 > prev.Y0:
		case h.Y0 == prev.Y0 && h.X0 >= prev.X1:
		default:
			t.Fatalf("hit %d at (%d,%d) overlaps or precedes hit %d at (%d,%d)",
				i, h.X0, h.Y0, i-1, prev.X0, prev.Y0)
		}
	}
}

// assertCursorIsOnARealRow: the cursor is an index into a list the render
// republishes every frame, so a stale one activates whatever moved into its
// slot.
func assertCursorIsOnARealRow(t *testing.T, m *OS) {
	t.Helper()
	if len(m.SidebarNav) == 0 {
		if m.SidebarCursor != 0 {
			t.Errorf("cursor %d on a rail with no navigable rows", m.SidebarCursor)
		}
		return
	}
	if m.SidebarCursor < 0 || m.SidebarCursor >= len(m.SidebarNav) {
		t.Errorf("cursor %d is outside the %d rows the frame published", m.SidebarCursor, len(m.SidebarNav))
	}
}

// TestRailSignatureMovesForDrawnStateAndNotForTheRest is the other half of the
// cache contract. An input the rows draw and the signature cannot see leaves a
// stale row on screen; a piece of state folded in that the rows never draw
// rebuilds the whole rail for nothing.
func TestRailSignatureMovesForDrawnStateAndNotForTheRest(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(tree)
	base := m.sidebarSignature()

	for _, tc := range []struct {
		name string
		set  func()
		want bool // true: the frame changes, so the signature must
	}{
		{"peek", func() { m.SidebarPeek = "api" }, true},
		{"agents filter", func() { m.SidebarAgentFilter = sidebarAgentsSession }, true},
		{"agents sort", func() { m.SidebarAgentSort = sidebarAgentsRecent }, true},
		{"collapsed", func() { m.SidebarCollapsed = true }, true},
		{"a workspace name", func() { m.WorkspaceNames = map[int]string{2: "review"} }, true},
		{"sessions scroll", func() { m.SidebarScrollS = 2 }, true},
		{"terminals scroll", func() { m.SidebarScrollT = 2 }, true},
		{"agents scroll", func() { m.SidebarScrollA = 2 }, true},

		// State the rail carries but never draws. Folding any of it in would
		// rebuild the rail on a mouse move that changed nothing on screen.
		{"the tooltip's latch", func() { m.Tooltip = tooltipState{Source: tooltipRailStrip, Key: 4} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := m.sidebarSignature()
			tc.set()
			after := m.sidebarSignature()
			if tc.want && after == before {
				t.Errorf("%s is drawn but not in the signature; the cache would serve the old rail", tc.name)
			}
			if !tc.want && after != before {
				t.Errorf("%s is in the signature but draws nothing; the rail rebuilds for nothing", tc.name)
			}
		})
	}
	_ = base
}

// TestRailEmptyStatesAreDocumented walks the three the design names, because
// each of them is a section that would otherwise silently say the opposite of
// the truth.
func TestRailEmptyStatesAreDocumented(t *testing.T) {
	// No agents anywhere: the section is absent, header included. A header over
	// nothing is furniture standing in for an alarm.
	m, _ := sectionsTestOS(t, 120, 30)
	for i := range m.Windows {
		m.Windows[i].AgentState = ""
	}
	quiet := railPlain(t, m, sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Focused: true, Workspace: 1},
		}},
	}))
	if lineOf(quiet, " agents") >= 0 {
		t.Error("the agents section drew a header with no agents behind it")
	}

	// A peek into a session with no panes says so, or the section reads as
	// "the attached session has no panes".
	m2, tree2 := sectionsTestOS(t, 120, 30)
	m2.SidebarPeek = "docs"
	if lineOf(railPlain(t, m2, tree2), "no terminals") < 0 {
		t.Error("an empty peek said nothing")
	}

	// A filter that hides everything says what it hid and offers the way back.
	m3, tree3 := sectionsTestOS(t, 120, 30)
	m3.SessionName = "docs" // attached to the session with no agents
	m3.SidebarAgentFilter = sidebarAgentsSession
	if lineOf(railPlain(t, m3, tree3), "none here") < 0 {
		t.Error("a filter that hid everything left the section blank")
	}
}
