package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

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
		want bool
	}{
		{"peek", func() { m.SidebarPeek = "api" }, true},
		{"agents filter", func() { m.SidebarAgentFilter = sidebarAgentsSession }, true},
		{"agents sort", func() { m.SidebarAgentSort = sidebarAgentsRecent }, true},
		{"collapsed", func() { m.SidebarCollapsed = true }, true},
		{"a workspace name", func() { m.WorkspaceNames = map[int]string{2: "review"} }, true},
		{"sessions scroll", func() { m.SidebarScroll[sidebarSectionSessions] = 2 }, true},
		{"terminals scroll", func() { m.SidebarScroll[sidebarSectionTerminals] = 2 }, true},
		{"agents scroll", func() { m.SidebarScroll[sidebarSectionAgents] = 2 }, true},
		{"the attached session's accent", func() { m.SessionAccent = "cyan" }, true},

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

// TestRailAddressingHoldsAcrossEveryCombination walks the product of position,
// collapse, peek, filter, sort and height, and asserts the three things that
// have to be true of every frame: hits and nav name the same targets in the
// same order, no rectangle escapes the band or overlaps its predecessor, and
// the cursor lands on a row that exists.
func TestRailAddressingHoldsAcrossEveryCombination(t *testing.T) {
	var tall, short int
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
								m.Settings = config.Global
								m.SidebarCollapsed = collapsed
								m.SidebarPeek = peek
								m.SidebarAgentFilter, m.SidebarAgentSort = filter, sortBy
								m.SidebarFocused = true
								m.sidebarPanelLinesForTree(tree)

								assertHitsFollowNav(t, m)
								assertHitsStayInTheBand(t, m)
								assertCursorIsOnARealRow(t, m)
								for _, hit := range m.SidebarHits {
									if hit.Kind != sidebarRowAgent {
										continue
									}
									if hit.Y1-hit.Y0 == sidebarAgentRowTall {
										tall++
									} else {
										short++
									}
								}
							})
						}
					}
				}
			}
		}
	}
	if tall == 0 || short == 0 {
		t.Errorf("the sweep drew %d tall agent rows and %d short ones; it is walking one height, not both", tall, short)
	}
}

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
		switch {
		case h.Y0 >= prev.Y1:
		case h.Y0 == prev.Y0 && h.X0 >= prev.X1:
		default:
			t.Fatalf("hit %d spans rows [%d,%d) and starts inside hit %d's [%d,%d)",
				i, h.Y0, h.Y1, i-1, prev.Y0, prev.Y1)
		}
	}
}

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
