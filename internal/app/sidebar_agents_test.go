package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// TestAgentsPriorityRankTable pins the section's own ordering against the
// roll-up's. They answer different questions and only one of them changed.
func TestAgentsPriorityRankTable(t *testing.T) {
	type row struct {
		state    string
		doneSeen bool
		want     int
	}
	for _, r := range []row{
		{"errored", false, 5},
		{"needs_input", false, 4},
		{"working", false, 3},
		{"done", false, 2},
		{"done", true, 1},
		{"idle", false, 0},
		{"", false, 0},
	} {
		if got := sidebarAgentPriority(r.state, r.doneSeen); got != r.want {
			t.Errorf("priority(%q, seen=%v) = %d, want %d", r.state, r.doneSeen, got, r.want)
		}
	}

	// The two orderings disagree on exactly one pair, and that disagreement is
	// the point: the rollup glyph may well want to say "something finished here".
	if sidebarAgentPriority("working", false) <= sidebarAgentPriority("done", false) {
		t.Error("the section sort must put working above done-unread")
	}
	if sessiontree.AgentRank("working", false) >= sessiontree.AgentRank("done", false) {
		t.Error("sessiontree.AgentRank was changed; the roll-up glyph is not the section sort")
	}
}

// TestAgentsSortByRecency checks the other order: newest first, whatever the
// state, because "what just happened" is a different question from "what is
// loudest".
func TestAgentsSortByRecency(t *testing.T) {
	m := &OS{Settings: config.Global, SidebarAgentSort: sidebarAgentsRecent}
	agents := []sidebarAgentEntry{
		{WindowID: "old", State: "errored", StateAt: 100},
		{WindowID: "new", State: "idle", StateAt: 300},
		{WindowID: "mid", State: "working", StateAt: 200},
	}
	m.sidebarSortAgents(agents)
	if agents[0].WindowID != "new" || agents[1].WindowID != "mid" || agents[2].WindowID != "old" {
		t.Errorf("recency order = %s/%s/%s, want new/mid/old",
			agents[0].WindowID, agents[1].WindowID, agents[2].WindowID)
	}
}

// TestAgentsFilterHere drops the panes of every session but the attached one,
// which is what a rail watching six sessions needs when only one of them is the
// one being worked in.
func TestAgentsFilterHere(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.SidebarAgentFilter = sidebarAgentsSession
	lines := railPlain(t, m, tree)

	if lineOf(lines, "server") >= 0 {
		t.Errorf("the here filter kept a foreign session's agent:\n%s", strings.Join(lines, "\n"))
	}
	if lineOf(lines, "build") < 0 {
		t.Errorf("the here filter dropped the attached session's own agent:\n%s", strings.Join(lines, "\n"))
	}
}

// TestAgentsFilterEmptyStateOffersTheWayBack: a section that vanished on a
// control set two days ago reads as "no agents anywhere", which is the opposite
// of the truth. One row says what is hidden and clicking it undoes the filter.
func TestAgentsFilterEmptyStateOffersTheWayBack(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	// Only the foreign session runs an agent that the "here" filter can hide.
	m.Windows[1].AgentState, m.Windows[2].AgentState = "", ""
	tree = sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Focused: true},
		}},
		{Name: "api", Windows: []sessiontree.WindowInput{
			{ID: "dddddddd4444", Title: "server", AgentState: "needs_input"},
		}},
	})
	m.SidebarAgentFilter = sidebarAgentsSession

	lines := railPlain(t, m, tree)
	hint := lineOf(lines, "none here")
	if hint < 0 {
		t.Fatalf("the emptied section said nothing:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[hint], "1 all") {
		t.Errorf("hint row %q does not say how many are hidden", lines[hint])
	}
	if lineOf(lines, " agents") < 0 {
		t.Error("the header went with the rows; then there is nothing to click to get them back")
	}

	// And it is a control, not a caption.
	var y int
	found := false
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowAgentFilter && h.SessionID != "" {
			y, found = h.Y0, true
		}
	}
	if !found {
		t.Fatal("the hint row has no hit rectangle, so it cannot be clicked")
	}
	m.SidebarClick(1, y, false)
	if m.sidebarAgentsFilter() != sidebarAgentsAll {
		t.Errorf("clicking the hint left the filter at %q", m.sidebarAgentsFilter())
	}
}

// TestAgentsHeaderTokensAreTheirOwnHitZones: two controls share the header line,
// so each has to claim only its own columns or one of them is unclickable.
func TestAgentsHeaderTokensAreTheirOwnHitZones(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(tree)

	var filter, sortHit *sidebarRowHit
	for i := range m.SidebarHits {
		switch m.SidebarHits[i].Kind {
		case sidebarRowAgentFilter:
			filter = &m.SidebarHits[i]
		case sidebarRowAgentSort:
			sortHit = &m.SidebarHits[i]
		}
	}
	if filter == nil || sortHit == nil {
		t.Fatal("the agents header drew no control zones")
	}
	if filter.Y0 != sortHit.Y0 {
		t.Errorf("the two tokens are on different lines: %d and %d", filter.Y0, sortHit.Y0)
	}
	if filter.X1 > sortHit.X0 {
		t.Errorf("the token zones overlap: filter [%d,%d), sort [%d,%d)",
			filter.X0, filter.X1, sortHit.X0, sortHit.X1)
	}

	// Each click reaches exactly its own control.
	m.SidebarClick(filter.X0, filter.Y0, false)
	if m.sidebarAgentsFilter() != sidebarAgentsSession || m.sidebarAgentsSort() != sidebarAgentsPriority {
		t.Errorf("clicking the filter gave filter=%q sort=%q", m.sidebarAgentsFilter(), m.sidebarAgentsSort())
	}
	m.SidebarClick(sortHit.X0, sortHit.Y0, false)
	if m.sidebarAgentsSort() != sidebarAgentsRecent || m.sidebarAgentsFilter() != sidebarAgentsSession {
		t.Errorf("clicking the sort gave filter=%q sort=%q", m.sidebarAgentsFilter(), m.sidebarAgentsSort())
	}
}

// TestAgentsHeaderIsSilentUntilAControlBites: a token at its default reads as
// furniture, a token that is changing the section reads as text.
func TestAgentsHeaderTokensRenderTheirState(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	lines := railPlain(t, m, tree)
	header := lineOf(lines, " agents")
	if header < 0 {
		t.Fatalf("no agents header:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[header], "all") || !strings.Contains(lines[header], "pri") {
		t.Errorf("default header does not carry its tokens: %q", lines[header])
	}

	m.SidebarAgentFilter, m.SidebarAgentSort = sidebarAgentsSession, sidebarAgentsRecent
	lines = railPlain(t, m, tree)
	header = lineOf(lines, " agents")
	if !strings.Contains(lines[header], "here") || !strings.Contains(lines[header], "rec") {
		t.Errorf("flipped header does not carry its tokens: %q", lines[header])
	}
}

// TestAgentsControlsAreInTheSignature: a control the cache cannot see leaves
// yesterday's rows, in yesterday's order, on the screen.
func TestAgentsControlsAreInTheSignature(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(tree)
	base := m.sidebarSignature()

	m.SidebarAgentFilter = sidebarAgentsSession
	filtered := m.sidebarSignature()
	if filtered == base {
		t.Error("the agents filter is not in the rail signature")
	}
	m.SidebarAgentSort = sidebarAgentsRecent
	if m.sidebarSignature() == filtered {
		t.Error("the agents sort is not in the rail signature")
	}
}

// TestAgentsControlsPersist: the two controls are shape the user chose, so they
// survive a restart beside the order and the width.
func TestAgentsControlsPersist(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m := &OS{Settings: config.Global}
	m.SidebarCycleAgentsFilter()
	m.SidebarCycleAgentsSort()

	restored := &OS{Settings: config.Global}
	restored.loadSidebarState()
	if restored.sidebarAgentsFilter() != sidebarAgentsSession || restored.sidebarAgentsSort() != sidebarAgentsRecent {
		t.Errorf("after a restart filter=%q sort=%q, want session/recent",
			restored.sidebarAgentsFilter(), restored.sidebarAgentsSort())
	}

	// Back to the defaults, which are written as empty and read back as defaults.
	m.SidebarCycleAgentsFilter()
	m.SidebarCycleAgentsSort()
	restored = &OS{Settings: config.Global}
	restored.loadSidebarState()
	if restored.sidebarAgentsFilter() != sidebarAgentsAll || restored.sidebarAgentsSort() != sidebarAgentsPriority {
		t.Errorf("after flipping back filter=%q sort=%q, want all/priority",
			restored.sidebarAgentsFilter(), restored.sidebarAgentsSort())
	}
}

// TestAgentsControlsDefaultOnAGarbageStateFile: the file is shared with whatever
// tuios the user runs next, and a value this build does not know must read back
// as the default rather than emptying the section.
func TestAgentsControlsDefaultOnAGarbageStateFile(t *testing.T) {
	m := &OS{Settings: config.Global, SidebarAgentFilter: "nonsense", SidebarAgentSort: "nonsense"}
	if m.sidebarAgentsFilter() != sidebarAgentsAll || m.sidebarAgentsSort() != sidebarAgentsPriority {
		t.Errorf("unknown values read back as filter=%q sort=%q", m.sidebarAgentsFilter(), m.sidebarAgentsSort())
	}
}
