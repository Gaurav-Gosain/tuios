package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Three sections with a filter, a sort, a peek and two rail states make the
// rail's two addressing lists far easier to pull apart than the tree did. These
// are the invariants that hold across every combination of them, not just the
// ones a feature's own test happened to render.

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
		{"the attached session's accent", func() { m.SessionAccent = "cyan" }, true},

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

// Three sections with a filter, a sort, a peek and two rail states make the
// rail's two addressing lists far easier to pull apart than the tree did. These
// are the invariants that hold across every combination of them, not just the
// ones a feature's own test happened to render.

// TestRailFitsAShortRegion walks every host height from nothing up to a rail
// that comfortably fits, on both sides.
//
// The heights above are the ones a rail is designed for; these are the ones it
// is squeezed into. Each section's header is drawn whether or not the budget
// could afford it, so once the chrome alone overran the region the rail emitted
// more lines than it had been given: the extra rows painted over the dock, and
// the hit rectangles recorded on them made a row outside the band clickable.
func TestRailFitsAShortRegion(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		for h := range 16 {
			t.Run(fmt.Sprintf("%s/h=%d", pos, h), func(t *testing.T) {
				m, tree := sectionsTestOS(t, 120, h)
				withSidebar(t, true, pos, config.SidebarDefaultWidth)
				m.Settings = config.Global
				// The expanded rail, which is the one that lays out sections; the
				// collapsed strip composes its own lines against the same height.
				m.SidebarCollapsed = false
				lines, _ := m.sidebarPanelLinesForTree(tree)

				if got, want := len(lines), m.GetUsableHeight(); got > want {
					t.Errorf("the rail drew %d rows into a region %d rows tall", got, want)
				}
				assertHitsFollowNav(t, m)
				assertHitsStayInTheBand(t, m)
				assertCursorIsOnARealRow(t, m)
			})
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
	if m.Settings.SidebarPosition == "right" {
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
		// Clearing the row above means clearing all of it. This was "starts on a
		// later line", which said the same thing while every row was one line
		// tall; an agent row is two, so a rectangle can now start after its
		// predecessor and still land inside it, which makes the row underneath
		// answer for clicks on the row above.
		switch {
		case h.Y0 >= prev.Y1:
		case h.Y0 == prev.Y0 && h.X0 >= prev.X1:
		default:
			t.Fatalf("hit %d spans rows [%d,%d) and starts inside hit %d's [%d,%d)",
				i, h.Y0, h.Y1, i-1, prev.Y0, prev.Y1)
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
