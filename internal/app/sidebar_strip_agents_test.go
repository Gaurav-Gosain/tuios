package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// The strip carries a second list under the spine: the agents group, pinned to
// the bottom above the controls and fenced off by a rule. These pin what it
// lists, in what order, what it looks like when there is nothing to list (the
// usual case, which has to stay silent), and what it does when there are more
// agents than lines.

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
		{Name: "api", Windows: []sessiontree.WindowInput{
			{ID: "dddddddd4444", Title: "server", AgentState: "needs_input"},
			{ID: "eeeeeeee5555", Title: "tests", AgentState: "errored"},
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

// TestStripStaysSilentWithNoAgents is the state the group is judged on, because
// it is the usual one: no rule, no rows, no reserved hole. The strip is exactly
// the spine it was before the group existed.
func TestStripStaysSilentWithNoAgents(t *testing.T) {
	m, tree := noAgentStripOS(t, 120, 20)
	lines := railPlain(t, m, tree)
	rule := config.GetWindowBorderLeft()

	want := []string{
		"  " + rule, // pad: nothing is blocked, so no badge
		"▎·" + rule,
		"  " + rule,
		" ·" + rule,
		"  " + rule,
		" ·" + rule,
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line %d = %q, want %q\n%s", i, lines[i], w, strings.Join(lines, "\n"))
		}
	}
	// Nothing between the spine and the two controls at the bottom.
	tail := []string{" +" + rule, " »" + rule, "  " + rule}
	for i, w := range tail {
		if got := lines[len(lines)-len(tail)+i]; got != w {
			t.Errorf("tail line %d = %q, want %q\n%s", i, got, w, strings.Join(lines, "\n"))
		}
	}
	for i := len(want); i < len(lines)-len(tail); i++ {
		if lines[i] != "  "+rule {
			t.Errorf("line %d = %q, want empty band all the way to the controls", i, lines[i])
		}
	}
	if joined := strings.Join(lines, "\n"); strings.Contains(joined, "─") {
		t.Errorf("the quiet strip drew the group's rule with no group behind it:\n%s", joined)
	}
	for _, n := range m.SidebarNav {
		if n.Kind == sidebarRowAgent {
			t.Errorf("the quiet strip recorded an agent target: %+v", n)
		}
	}
}

// TestStripGroupListsWhatWantsAHumanInTheRailsOwnOrder: the group is the
// expanded agents section folded, so it cannot rank two panes differently from
// the open rail, and it drops the two ranks that would stand a permanent group
// under the spine saying nothing.
func TestStripGroupListsWhatWantsAHumanInTheRailsOwnOrder(t *testing.T) {
	m, tree := agentStripOS(t, 120, 40)
	m.sidebarPanelLinesForTree(tree)
	var strip []string
	for _, r := range stripGroupRows(m) {
		strip = append(strip, r.WindowID)
	}

	want := []string{"eeeeeeee5555", "dddddddd4444", "aaaaaaaa1111", "bbbbbbbb2222"}
	if strings.Join(strip, ",") != strings.Join(want, ",") {
		t.Errorf("the group lists %v, want blocked, then working, then done-unread", strip)
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
}

// TestStripGroupSitsUnderARuleAtTheBottom: the group is told apart from the
// spine by a drawn boundary and by where it is anchored, not by a second
// interval, because one interval is what makes the whole strip scan as lists.
func TestStripGroupSitsUnderARuleAtTheBottom(t *testing.T) {
	m, tree := agentStripOS(t, 120, 24)
	lines := railPlain(t, m, tree)
	rule := config.GetWindowBorderLeft()

	// Four agents, spaced like the spine, over the blank that holds the group off
	// the controls, then the toggle.
	tail := []string{
		"──" + rule,
		" ×" + rule, "  " + rule,
		" ▲" + rule, "  " + rule,
		"▎●" + rule, "  " + rule, // the working pane is the focused one
		" ■" + rule, "  " + rule,
		" +" + rule,
		" »" + rule,
		"  " + rule,
	}
	for i, w := range tail {
		if got := lines[len(lines)-len(tail)+i]; got != w {
			t.Errorf("tail line %d = %q, want %q\n%s", i, got, w, strings.Join(lines, "\n"))
		}
	}
	// The rule is furniture, so it takes no target; every mark under it does.
	if got := len(stripGroupRows(m)); got != 4 {
		t.Errorf("%d agent rows recorded, want one per mark drawn", got)
	}
}

// TestStripGroupTargetsOwnTheirSlots: an agent row is two cells of glyph on a
// three-column rail, so its rectangle has to be the whole band and the whole
// slot, at both first and last cell, on both sides of the screen.
func TestStripGroupTargetsOwnTheirSlots(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		m, tree := agentStripOS(t, 120, 24)
		withSidebar(t, true, pos, config.SidebarDefaultWidth)
		m.SidebarCollapsed = true
		m.SidebarFocused = true
		m.sidebarPanelLinesForTree(tree)

		w := m.GetSidebarWidth()
		railX0 := 0
		if pos == "right" {
			railX0 = m.GetRenderWidth() - w
		}
		rows := stripGroupRows(m)
		if len(rows) != 4 {
			t.Fatalf("%s: %d agent rows, want 4", pos, len(rows))
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
		spine, group, rule       int
	}{
		{20, 3, 2, 15, 4, 1}, // room to spare: the spine keeps the slack
		{9, 3, 2, 4, 4, 1},   // exactly both
		{6, 3, 2, 3, 2, 1},   // tight: the group takes half and no more
		{4, 3, 4, 2, 1, 1},   // one line of group left
		{3, 3, 4, 3, 0, 0},   // no room for a rule and a mark: spine alone
		{20, 3, 0, 20, 0, 0}, // no agents: no rule, no hole
	} {
		spine, group, rule := sidebarStripSplit(tc.region, tc.sessions, tc.agents)
		if spine != tc.spine || group != tc.group || rule != tc.rule {
			t.Errorf("split(region=%d s=%d a=%d) = %d/%d/%d, want %d/%d/%d",
				tc.region, tc.sessions, tc.agents, spine, group, rule, tc.spine, tc.group, tc.rule)
		}
		if spine+group+rule > tc.region {
			t.Errorf("split(region=%d s=%d a=%d) claims %d rows", tc.region, tc.sessions, tc.agents, spine+group+rule)
		}
	}

	// Off the frame: eight agents on a rail with room for two of them keeps the
	// sessions, packs what it can, and ends on the tail mark.
	m, _ := sectionsTestOS(t, 120, 16)
	m.SidebarCollapsed = true
	var wins []sessiontree.WindowInput
	for i := range 8 {
		wins = append(wins, sessiontree.WindowInput{ID: string(rune('a'+i)) + "gent", AgentState: "working"})
	}
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, Windows: wins},
		{Name: "api"},
	})
	lines := railPlain(t, m, tree)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "⋮") {
		t.Errorf("the overflowing group drew no tail mark:\n%s", joined)
	}
	if n := strings.Count(joined, "·"); n != 2 {
		t.Errorf("the spine lost a session to the group: %d dots, want 2\n%s", n, joined)
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

// TestStripGroupASCIIAndMonochrome: the rule and the marks both have to survive
// a terminal with no box drawing and one with no colour, because the group's
// whole job is to be readable at a glance.
func TestStripGroupASCIIAndMonochrome(t *testing.T) {
	t.Run("ascii", func(t *testing.T) {
		prev := config.UseASCIIOnly
		config.UseASCIIOnly = true
		overlay.SetASCII(true)
		t.Cleanup(func() {
			config.UseASCIIOnly = prev
			overlay.SetASCII(prev)
		})

		m, tree := agentStripOS(t, 120, 24)
		joined := strings.Join(railPlain(t, m, tree), "\n")
		if strings.Contains(joined, "─") || !strings.Contains(joined, "--") {
			t.Errorf("the ASCII group's rule is missing:\n%s", joined)
		}
		for _, glyph := range []string{"×", "▲", "●", "■", "▎"} {
			if strings.Contains(joined, glyph) {
				t.Errorf("the ASCII group still draws %q:\n%s", glyph, joined)
			}
		}
	})

	t.Run("monochrome", func(t *testing.T) {
		// Monochrome is the frame with every colour dropped: the marks differ by
		// shape, the rule by being a glyph rather than a fill.
		m, tree := agentStripOS(t, 120, 24)
		joined := strings.Join(railPlain(t, m, tree), "\n")
		for _, want := range []string{"──", "×", "▲", "●", "■"} {
			if !strings.Contains(joined, want) {
				t.Errorf("monochrome loses %q:\n%s", want, joined)
			}
		}
	})
}

// TestStripGroupKeepsHitsAndNavIndexForIndex with the group present and absent,
// on both sides: the two addressing lists are what a click and the keyboard
// resolve through, and a new section is exactly what pulls them apart.
func TestStripGroupKeepsHitsAndNavIndexForIndex(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		for _, withAgents := range []bool{false, true} {
			build := noAgentStripOS
			if withAgents {
				build = agentStripOS
			}
			m, tree := build(t, 120, 24)
			withSidebar(t, true, pos, config.SidebarDefaultWidth)
			m.SidebarCollapsed = true
			m.sidebarPanelLinesForTree(tree)

			if len(m.SidebarHits) != len(m.SidebarNav) {
				t.Fatalf("%s/agents=%v: %d hits against %d nav rows", pos, withAgents, len(m.SidebarHits), len(m.SidebarNav))
			}
			lastSession := -1
			for i, h := range m.SidebarHits {
				n := m.SidebarNav[i]
				if h.Kind != n.Kind || h.SessionID != n.SessionID || h.WindowID != n.WindowID {
					t.Errorf("%s/agents=%v: hit %d is %v/%q/%q, nav is %v/%q/%q",
						pos, withAgents, i, h.Kind, h.SessionID, h.WindowID, n.Kind, n.SessionID, n.WindowID)
				}
				if h.Kind == sidebarRowSession {
					lastSession = i
				}
			}
			// The group is drawn under the spine, so its rows are recorded after
			// it: the two lists are one sequence, in drawn order.
			for _, r := range stripGroupRows(m) {
				for i, h := range m.SidebarHits {
					if h.Y0 == r.Y0 && i < lastSession {
						t.Errorf("%s: the group's row at %d is recorded before the spine's", pos, r.Y0)
					}
				}
			}
		}
	}
}
