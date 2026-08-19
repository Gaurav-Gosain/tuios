package app

import (
	"fmt"
	"strings"
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

// railAgentRowIDs is the windows the agents section actually drew, in the order
// it drew them. Read off the hit rectangles the renderer recorded, which is the
// only account of what reached the screen.
func railAgentRowIDs(m *OS) []string {
	var out []string
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowAgent {
			out = append(out, h.WindowID)
		}
	}
	return out
}

// TestRailAgentsViewportHoldsItsRowWhenTheSortReorders is the one with real
// correctness content: a pane below the fold asking for a human hoists itself to
// the top of a priority-sorted list, and the reader must not be moved by it.
// Both addressing lists are checked, because they are two different mechanisms:
// the cursor is re-anchored by identity in sidebarPublishNav, the viewport in
// sidebarReanchorAgents, and either alone leaves the reader half-moved.
func TestRailAgentsViewportHoldsItsRowWhenTheSortReorders(t *testing.T) {
	const panes = 12
	m, _ := sectionsTestOS(t, 120, 30)
	m.SidebarFocused = true

	calm := agentsScrollTree(panes, nil)
	m.sidebarPanelLinesForTree(calm)
	m.SidebarScrollA = 2
	m.sidebarPanelLinesForTree(calm)

	before := railAgentRowIDs(m)
	if len(before) < 3 {
		t.Fatalf("the agents section drew %d rows, too few to scroll under a reader", len(before))
	}

	// The cursor rests one row into the visible block, which is where a reader
	// steering with j/k leaves it.
	held := before[1]
	for i, r := range m.SidebarNav {
		if r.Kind == sidebarRowAgent && r.WindowID == held {
			m.SidebarCursor = i
		}
	}

	// A pane the reader cannot see asks for a human. Priority puts it first, and
	// every row the reader was looking at moves down one.
	loudIdx := panes - 1
	if strings.Contains(strings.Join(before, " "), fmt.Sprintf("pane-%02d", loudIdx)) {
		t.Fatalf("the pane meant to be below the fold was on screen: %v", before)
	}
	m.sidebarPanelLinesForTree(agentsScrollTree(panes, map[int]string{loudIdx: "needs_input"}))

	after := railAgentRowIDs(m)
	if len(after) == 0 {
		t.Fatal("the agents section drew nothing after the reorder")
	}
	if after[0] != before[0] {
		t.Errorf("the agents viewport moved under the reader: it was showing %v, now %v", before, after)
	}
	row, ok := m.sidebarCursorRow()
	if !ok || row.WindowID != held {
		t.Errorf("the cursor followed the index instead of the row: was on %s, now on %+v", held, row)
	}
}

// TestRailAgentsAtTheTopStayAtTheTop: the top of a priority-sorted list is where
// the loudest thing is, so a reader who has not scrolled must see the pane that
// just started asking for a human, not the row that used to be first.
func TestRailAgentsAtTheTopStayAtTheTop(t *testing.T) {
	const panes = 12
	m, _ := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(agentsScrollTree(panes, nil))
	if m.SidebarScrollA != 0 {
		t.Fatalf("the agents section did not start at the top: offset %d", m.SidebarScrollA)
	}

	loud := fmt.Sprintf("pane-%02d", panes-1)
	m.sidebarPanelLinesForTree(agentsScrollTree(panes, map[int]string{panes - 1: "needs_input"}))
	if got := railAgentRowIDs(m); len(got) == 0 || got[0] != loud {
		t.Errorf("the section at the top drew %v first, want the pane asking for a human (%s)", got, loud)
	}
}

// TestRailAgentsAnchorYieldsToAScroll: the anchor speaks for an offset nothing
// else has touched. A wheel is the reader saying where to look, and it outranks
// what the reader was looking at a moment ago.
func TestRailAgentsAnchorYieldsToAScroll(t *testing.T) {
	const panes = 12
	m, _ := sectionsTestOS(t, 120, 30)
	calm := agentsScrollTree(panes, nil)
	m.sidebarPanelLinesForTree(calm)
	m.SidebarScrollA = 2
	m.sidebarPanelLinesForTree(calm)
	first := railAgentRowIDs(m)[0]

	// A wheel over the section, then a frame with the same rows in the same order.
	m.SidebarScrollA = 4
	m.sidebarPanelLinesForTree(calm)
	if got := railAgentRowIDs(m)[0]; got == first {
		t.Errorf("the anchor dragged the viewport back to %s after a scroll", got)
	}
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
