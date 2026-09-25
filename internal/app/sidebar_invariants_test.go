package app

import (
	"testing"
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
