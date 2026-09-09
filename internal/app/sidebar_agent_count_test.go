package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// railAgentsHeader is the rendered agents header line, stripped of styling.
func railAgentsHeader(t *testing.T, lines []string) string {
	t.Helper()
	for _, ln := range lines {
		plain := stripANSIForTrace(ln)
		if strings.Contains(plain, "agents") {
			return plain
		}
	}
	t.Fatalf("no agents header drawn:\n%s", strings.Join(lines, "\n"))
	return ""
}

// TestAgentsHeaderCountsBlockedAndDone: the expanded agents header says how
// many of the listed panes want a human and how many finished unread, in
// front of the filter and sort tokens.
func TestAgentsHeaderCountsBlockedAndDone(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 40)
	m.SidebarWidthPref = 50 // wide enough for the words beside the mail glyph
	lines, _ := m.sidebarPanelLinesForTree(tree)
	header := railAgentsHeader(t, lines)
	want := sidebarAgentCountText(1, 1)
	if !strings.Contains(header, want) {
		t.Fatalf("agents header = %q, want it to carry %q", header, want)
	}
	if strings.Index(header, "1 blocked") > strings.Index(header, "all") {
		t.Fatalf("the count follows the filter token: %q", header)
	}

	// The count is over the rows the section lists. The attached session has
	// the done pane and not the blocked one, so "here" shows done alone.
	m.SidebarAgentFilter = sidebarAgentsSession
	lines, _ = m.sidebarPanelLinesForTree(tree)
	header = railAgentsHeader(t, lines)
	if strings.Contains(header, "blocked") || !strings.Contains(header, "1 done") {
		t.Fatalf("with the filter on the header = %q, want \"1 done\" alone", header)
	}

	// A finished pane that has been looked at is not done in the rail's
	// sense: seeing it is what dims it, and it leaves the count with it.
	m.SidebarAgentFilter = sidebarAgentsAll
	for i := range tree.Sessions[0].Children {
		if tree.Sessions[0].Children[i].AgentState == "done" {
			tree.Sessions[0].Children[i].DoneSeen = true
		}
	}
	lines, _ = m.sidebarPanelLinesForTree(tree)
	header = railAgentsHeader(t, lines)
	if strings.Contains(header, "done") || !strings.Contains(header, "1 blocked") {
		t.Fatalf("with the done pane seen the header = %q, want \"1 blocked\" alone", header)
	}

	// A section with nothing to count says nothing.
	for _, s := range tree.Sessions {
		for i := range s.Children {
			if s.Children[i].AgentState != "" {
				s.Children[i].AgentState = "working"
			}
		}
	}
	lines, _ = m.sidebarPanelLinesForTree(tree)
	if header = railAgentsHeader(t, lines); strings.Contains(header, "blocked") || strings.Contains(header, "done") {
		t.Fatalf("a section with only working panes counts something: %q", header)
	}
}

// TestAgentsHeaderCountIsAFilterTarget: the count has a hit rectangle of its
// own and clicking it cycles the filter, the same thing the filter token does.
func TestAgentsHeaderCountIsAFilterTarget(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 40)
	m.SidebarWidthPref = 50
	m.sidebarPanelLinesForTree(tree)
	var count *sidebarRowHit
	for i := range m.SidebarHits {
		if m.SidebarHits[i].Kind == sidebarRowAgentFilter && m.SidebarHits[i].WindowID == sidebarCountTokenID {
			count = &m.SidebarHits[i]
		}
	}
	if count == nil {
		t.Fatal("the count token recorded no hit rectangle")
	}
	if got, want := count.X1-count.X0, lipgloss.Width(sidebarAgentCountText(1, 1)); got != want {
		t.Fatalf("the count's rectangle spans %d cells, want the text's width %d", got, want)
	}
	if !m.SidebarClick(count.X0, count.Y0, false) {
		t.Fatal("a click on the count was not consumed")
	}
	if m.sidebarAgentsFilter() != sidebarAgentsSession {
		t.Fatalf("clicking the count left the filter on %q, want here", m.sidebarAgentsFilter())
	}
}

// TestAgentsHeaderCountGivesWayInSteps: at the default width the words do
// not fit beside the controls and the mail token, so the header carries the
// blocked figure alone in the strip badge's glyph form, and the mail token
// stays; with unread mail widening that token, the count goes before it does.
func TestAgentsHeaderCountGivesWayInSteps(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 40)
	lines, _ := m.sidebarPanelLinesForTree(tree)
	header := railAgentsHeader(t, lines)
	info := sidebarAgentCountInfo{Blocked: 1, Done: 1, Worst: "needs_input"}
	if strings.Contains(header, "blocked") || strings.Contains(header, info.glyphs(false)) ||
		!strings.Contains(header, info.glyphs(true)) {
		t.Fatalf("the default-width header = %q, want the blocked figure alone, %q", header, info.glyphs(true))
	}
	if !strings.Contains(header, sidebarMailGlyph()) {
		t.Fatalf("the mail token yielded to the count: %q", header)
	}
	if !strings.Contains(header, "all") || !strings.Contains(header, "pri") {
		t.Fatalf("the header lost its controls: %q", header)
	}

	m.AgentMail.Messages = []session.AgentMessage{
		{ID: 1, Kind: "message", To: session.AgentInboxHuman},
		{ID: 2, Kind: "message", To: session.AgentInboxHuman},
	}
	lines, _ = m.sidebarPanelLinesForTree(tree)
	header = railAgentsHeader(t, lines)
	if !strings.Contains(header, sidebarMailGlyph()+" 2") {
		t.Fatalf("mail with something unread yielded to the count: %q", header)
	}
	if strings.Contains(header, info.glyphs(true)) {
		t.Fatalf("the count did not yield to unread mail: %q", header)
	}

	// A rail two cells wider than the default fits both figures.
	m.AgentMail.Messages = nil
	m.SidebarWidthPref = 31
	lines, _ = m.sidebarPanelLinesForTree(tree)
	if header = railAgentsHeader(t, lines); !strings.Contains(header, info.glyphs(false)) {
		t.Fatalf("a slightly wider rail = %q, want both figures %q", header, info.glyphs(false))
	}
}
