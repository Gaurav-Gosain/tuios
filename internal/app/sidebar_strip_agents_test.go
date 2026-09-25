package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// The strip carries a third list under the sessions and the terminals: the
// agents group, headed by its own name and listing what wants a human somewhere
// the two lists above it are not already showing. These pin what it lists, in
// what order, what it looks like when there is nothing to list (the usual case,
// which has to stay silent), and what it does when there are more agents than
// lines.

// agentStripOS is a collapsed rail over one agent of every rank: an errored and
// a blocked pane in another session, one working and one finished-unread in the
// attached one, plus the two ranks the group drops.
func agentStripOS(t *testing.T, w, h int) (*OS, sessiontree.Tree) {
	t.Helper()
	m, _ := sectionsTestOS(t, w, h)
	m.SidebarCollapsed = true
	m.FocusedWindow = 0
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Focused: true, AgentState: "working"},
			{ID: "bbbbbbbb2222", Title: "refactor", AgentState: "done"},
			{ID: "cccccccc3333", Title: "build", AgentState: "done", DoneSeen: true},
			{ID: "ffffffff6666", Title: "shell", AgentState: "idle"},
		}},
		// The errored pane was opened first. The strip lists in the section's
		// order, which by default keeps spawn order inside the needs-you
		// group, and several tests below read the errored pane as the first.
		{Name: "api", Windows: []sessiontree.WindowInput{
			{ID: "eeeeeeee5555", Title: "tests", AgentState: "errored"},
			{ID: "dddddddd4444", Title: "server", AgentState: "needs_input"},
		}},
		{Name: "docs"},
	})
	return m, tree
}

// noAgentStripOS is the strip as it is nearly all the time: sessions, no agent
// anywhere with anything to say.
func noAgentStripOS(t *testing.T, w, h int) (*OS, sessiontree.Tree) {
	t.Helper()
	m, _ := sectionsTestOS(t, w, h)
	m.SidebarCollapsed = true
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Focused: true},
			{ID: "cccccccc3333", Title: "build", AgentState: "done", DoneSeen: true},
		}},
		{Name: "api", Windows: []sessiontree.WindowInput{{ID: "dddddddd4444", Title: "server"}}},
		{Name: "docs"},
	})
	return m, tree
}

// stripGroupRows is the agents group as the renderer recorded it. The badge
// addresses a pane too, so the group's own rows are what tell the two apart.
func stripGroupRows(m *OS) []sidebarStripRow {
	var out []sidebarStripRow
	for _, r := range m.sidebarStripRows {
		if r.Kind == sidebarStripAgent {
			out = append(out, r)
		}
	}
	return out
}

// stripNavWindows is the nav list's window IDs for one row kind, which is what
// the two rail states have to agree on.
func stripNavWindows(m *OS, kind sidebarRowKind) []string {
	var out []string
	for _, n := range m.SidebarNav {
		if n.Kind == kind {
			out = append(out, n.WindowID)
		}
	}
	return out
}

// TestStripGroupListsWhatWantsAHumanInTheRailsOwnOrder: the group is the
// expanded agents section folded, so it cannot rank two panes differently from
// the open rail. It drops the two ranks that would stand a permanent group
// under the others saying nothing, and the panes the terminals list above it is
// already drawing.
func TestStripGroupListsWhatWantsAHumanInTheRailsOwnOrder(t *testing.T) {
	m, tree := agentStripOS(t, 120, 40)
	m.sidebarPanelLinesForTree(tree)
	var strip []string
	for _, r := range stripGroupRows(m) {
		strip = append(strip, r.WindowID)
	}

	want := []string{"eeeeeeee5555", "dddddddd4444"}
	if strings.Join(strip, ",") != strings.Join(want, ",") {
		t.Errorf("the group lists %v, want the errored pane elsewhere, then the blocked one", strip)
	}

	// The same list the expanded rail publishes, minus the ranks the strip drops,
	// in the same relative order.
	m.SidebarCollapsed = false
	m.sidebarPanelLinesForTree(tree)
	expanded := stripNavWindows(m, sidebarRowAgent)
	j := 0
	for _, id := range strip {
		for j < len(expanded) && expanded[j] != id {
			j++
		}
		if j >= len(expanded) {
			t.Fatalf("the strip's order %v is not the expanded rail's %v", strip, expanded)
		}
		j++
	}
	for _, id := range []string{"cccccccc3333", "ffffffff6666"} {
		if strings.Contains(strings.Join(strip, ","), id) {
			t.Errorf("the group kept %q; a reviewed or idle agent has nothing to say at two columns", id)
		}
	}
	for _, id := range []string{"aaaaaaaa1111", "bbbbbbbb2222"} {
		if strings.Contains(strings.Join(strip, ","), id) {
			t.Errorf("the group repeated %q; the terminals list above it already draws that pane", id)
		}
	}
}

// TestStripGroupTargetsOwnTheirSlots: an agent row is two cells of glyph on a
// three-column rail, so its rectangle has to be the whole band and the whole
// slot, at both first and last cell, on both sides of the screen.
func TestStripGroupTargetsOwnTheirSlots(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		m, tree := agentStripOS(t, 120, 24)
		withSidebar(t, true, pos, config.SidebarDefaultWidth)
		m.Settings = config.Global
		m.SidebarCollapsed = true
		m.SidebarFocused = true
		m.sidebarPanelLinesForTree(tree)

		w := m.GetSidebarWidth()
		railX0 := 0
		if pos == "right" {
			railX0 = m.GetRenderWidth() - w
		}
		rows := stripGroupRows(m)
		if len(rows) != 2 {
			t.Fatalf("%s: %d agent rows, want 2", pos, len(rows))
		}
		for _, r := range rows {
			h, ok := m.sidebarRowAt(railX0, r.Y0)
			if !ok || h.Kind != sidebarRowAgent {
				t.Fatalf("%s: the agent row at %d hits %+v", pos, r.Y0, h)
			}
			if h.X0 != railX0 || h.X1 != railX0+w || h.Y0 != r.Y0 || h.Y1 != r.Y1 {
				t.Errorf("%s: agent %q claims %d..%d x rows %d..%d, want the band and its whole slot",
					pos, h.WindowID, h.X0, h.X1, h.Y0, h.Y1)
			}
			for y := h.Y0; y < h.Y1; y++ {
				for x := h.X0; x < h.X1; x++ {
					if got, ok := m.sidebarRowAt(x, y); !ok || got.WindowID != h.WindowID {
						t.Fatalf("%s: cell (%d,%d) of agent %q resolves to %+v", pos, x, y, h.WindowID, got)
					}
				}
			}
			// A click anywhere in the slot focuses that pane. Only the panes of the
			// attached session can be proved that far here: the rest need a daemon
			// to switch through first.
			idx := m.windowIndexByID(h.WindowID)
			if idx < 0 {
				continue
			}
			m.FocusedWindow = -1
			m.SidebarClick(h.X0+1, h.Y1-1, false)
			if m.FocusedWindow != idx {
				t.Errorf("%s: clicking the last row of agent %q focused %d, want %d",
					pos, h.WindowID, m.FocusedWindow, idx)
			}
		}
	}
}

// TestStripGroupTooltipNamesTheAgent: two cells cannot say which pane this is,
// so the label does, through the same primitive the spine's rows use.
func TestStripGroupTooltipNamesTheAgent(t *testing.T) {
	m, tree := agentStripOS(t, 120, 24)
	m.sidebarPanelLinesForTree(tree)

	var row sidebarStripRow
	for _, r := range m.sidebarStripRows {
		if r.Kind == sidebarStripAgent {
			row = r
			break
		}
	}
	if !strings.Contains(row.Label, "api/tests") || !strings.Contains(row.Label, "errored") {
		t.Fatalf("the first agent row's label is %q, want the pane, its session and its state", row.Label)
	}
	// Anywhere in the slot, not only on the mark's own line.
	for y := row.Y0; y < row.Y1; y++ {
		m.tooltipClear()
		m.sidebarTooltipTrack(1, y)
		m.Tooltip.At = m.Tooltip.At.Add(-2 * tooltipDelay)
		if m.renderRailTooltip() == nil {
			t.Errorf("hovering row %d of the agent's slot popped no tooltip", y)
		}
	}
}

// TestStripGroupYieldsToTheSpineWhenShort: a burst of agents cannot push the
// sessions off the rail, and the group says what it could not draw rather than
// stopping silently.
func TestStripGroupYieldsToTheSpineWhenShort(t *testing.T) {
	for _, tc := range []struct {
		region, sessions, agents int
		wantSessions, wantAgents int
		wantGroups               int
	}{
		{20, 3, 2, 3, 2, 2}, // room to spare: both lists whole
		{8, 3, 2, 3, 2, 2},  // exactly both, headers included
		{6, 3, 2, 1, 2, 2},  // tight: the longer list gives the row back
		{5, 3, 2, 3, 0, 1},  // no room for a header and two marks: sessions alone
		{20, 3, 0, 3, 0, 1}, // no agents: no group, no hole
	} {
		groups := []sidebarStripGroup{
			{kind: sidebarStripSession, noun: "session", total: tc.sessions},
		}
		if tc.agents > 0 {
			groups = append(groups, sidebarStripGroup{kind: sidebarStripAgent, noun: "agent", total: tc.agents})
		}
		placed := sidebarStripPlace(groups, 0, tc.region)
		if len(placed) != tc.wantGroups {
			t.Errorf("place(region=%d s=%d a=%d) kept %d groups, want %d",
				tc.region, tc.sessions, tc.agents, len(placed), tc.wantGroups)
			continue
		}
		got := map[sidebarStripRowKind]int{}
		last := 0
		for _, g := range placed {
			got[g.kind] = g.shown()
			last = max(last, g.end)
			if g.moreY >= 0 {
				last = max(last, g.moreY+1)
			}
		}
		if got[sidebarStripSession] != tc.wantSessions || got[sidebarStripAgent] != tc.wantAgents {
			t.Errorf("place(region=%d s=%d a=%d) drew %d sessions and %d agents, want %d and %d",
				tc.region, tc.sessions, tc.agents, got[sidebarStripSession], got[sidebarStripAgent],
				tc.wantSessions, tc.wantAgents)
		}
		if last > tc.region {
			t.Errorf("place(region=%d s=%d a=%d) claims %d rows", tc.region, tc.sessions, tc.agents, last)
		}
	}

	// Off the frame: eight agents in another session, on a rail with room for a
	// few of them. The sessions and the terminals keep their rows, the group
	// packs what it can, and it ends on the tail mark.
	m, _ := sectionsTestOS(t, 120, 16)
	m.SidebarCollapsed = true
	var wins []sessiontree.WindowInput
	for i := range 8 {
		wins = append(wins, sessiontree.WindowInput{ID: string(rune('a'+i)) + "gent", AgentState: "working"})
	}
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, Windows: []sessiontree.WindowInput{
			{ID: "here1111", Title: "shell", Focused: true},
		}},
		{Name: "api", Windows: wins},
	})
	lines := railPlain(t, m, tree)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "⋮") {
		t.Errorf("the overflowing group drew no tail mark:\n%s", joined)
	}
	if n := strings.Count(joined, "·"); n != 3 {
		t.Errorf("a list lost a row to the group: %d dots, want the two sessions and the one pane\n%s", n, joined)
	}
	var tail sidebarStripRow
	for _, r := range m.sidebarStripRows {
		if r.Kind == sidebarStripMore {
			tail = r
		}
	}
	if !strings.Contains(tail.Label, "more agents") {
		t.Errorf("the tail mark says %q, want the agents it stands in for", tail.Label)
	}
	hit, ok := m.sidebarRowAt(1, tail.Y0)
	if !ok || hit.Kind != sidebarRowCollapse {
		t.Errorf("the group's tail mark hits %+v, want the expand target", hit)
	}
}

// TestStripGroupASCIIAndMonochrome: the header and the marks both have to
// survive a terminal with no box drawing and one with no colour, because the
// group's
// whole job is to be readable at a glance.
func TestStripGroupASCIIAndMonochrome(t *testing.T) {
	t.Run("ascii", func(t *testing.T) {
		prev := config.Global.UseASCIIOnly
		config.Global.UseASCIIOnly = true
		overlay.SetASCII(true)
		t.Cleanup(func() {
			config.Global.UseASCIIOnly = prev
			overlay.SetASCII(prev)
		})

		m, tree := agentStripOS(t, 120, 24)
		joined := strings.Join(railPlain(t, m, tree), "\n")
		if !strings.Contains(joined, "a") {
			t.Errorf("the ASCII group lost the name that tells it apart:\n%s", joined)
		}
		for _, glyph := range []string{"×", "▲", "●", "■", "▎"} {
			if strings.Contains(joined, glyph) {
				t.Errorf("the ASCII group still draws %q:\n%s", glyph, joined)
			}
		}
	})

	t.Run("monochrome", func(t *testing.T) {
		// Monochrome is the frame with every colour dropped: the marks differ by
		// shape, and the group by the name over it.
		m, tree := agentStripOS(t, 120, 24)

		m.Settings = config.Global
		joined := strings.Join(railPlain(t, m, tree), "\n")
		for _, want := range []string{" a", "×", "▲", "●", "■"} {
			if !strings.Contains(joined, want) {
				t.Errorf("monochrome loses %q:\n%s", want, joined)
			}
		}
	})
}
