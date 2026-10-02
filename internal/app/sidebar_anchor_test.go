package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// The agents section is priority-sorted by default, so its order is a function
// of live agent state: a pane going blocked thirty rows down moves to the top
// and pushes every row under the reader down one. These are the claims about
// what the reader keeps when that happens.

// agentsScrollTree is one attached session of n panes all running an agent,
// with the states of the ones named in loud overridden. Every other pane is
// working, so the priority sort leaves them in tree order and the only thing
// that moves a row is a state the test set.
func agentsScrollTree(n int, loud map[int]string) sessiontree.Tree {
	windows := make([]sessiontree.WindowInput, 0, n)
	for i := range n {
		state := "working"
		if s, ok := loud[i]; ok {
			state = s
		}
		windows = append(windows, sessiontree.WindowInput{
			ID:         fmt.Sprintf("pane-%02d", i),
			Title:      fmt.Sprintf("pane-%02d", i),
			AgentState: state,
			Workspace:  1,
			Focused:    i == 0,
		})
	}
	return sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, CurrentWorkspace: 1, Windows: windows},
	})
}

// TestRailAgentsAnchorSurvivesItsRowVanishing: an anchor naming a pane that has
// been closed clamps rather than resetting the section to the top, and the rail
// still addresses what it drew.
func TestRailAgentsAnchorSurvivesItsRowVanishing(t *testing.T) {
	m, _ := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(agentsScrollTree(12, nil))
	m.SidebarScrollA = 3
	m.sidebarPanelLinesForTree(agentsScrollTree(12, nil))

	// Every pane the anchor could name goes at once.
	m.sidebarPanelLinesForTree(agentsScrollTree(2, nil))
	if m.SidebarScrollA != 0 {
		t.Errorf("the agents scroll clamped to %d over a 2-row section, want 0", m.SidebarScrollA)
	}
	assertHitsFollowNav(t, m)
	assertHitsStayInTheBand(t, m)
	assertCursorIsOnARealRow(t, m)
}

// TestRailAgentsAnchorIsInTheSignature: the anchor decides the offset the next
// frame draws from, so a frame drawn under one anchor must not be served from a
// cache entry keyed on another.
func TestRailAgentsAnchorIsInTheSignature(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(tree)
	before := m.sidebarSignature()
	m.sidebarAgentAnchor = sidebarScrollAnchor{SessionID: "main", WindowID: "bbbbbbbb2222", Offset: 1, Valid: true}
	if m.sidebarSignature() == before {
		t.Error("the agents scroll anchor picks the offset the rows are drawn from but is not in the rail signature")
	}
}
