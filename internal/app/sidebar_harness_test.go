package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The rail could show eight agents running and not say which of them was
// Claude. The harness was detected, merged and carried on the live window, and
// stopped one layer short of the wire; these are the claims about what it says
// now that it reaches the row.

// railAgentRow returns the rendered lines of the agents-section row for a
// window, joined, or "" when the rail drew none. A row is one line or two, and
// which one a fact landed on is the layout's business rather than these tests'.
func railAgentRow(m *OS, lines []string, windowID string) string {
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowAgent && h.WindowID == windowID {
			top := h.Y0 - m.GetTopMargin()
			return strings.Join(lines[top:min(h.Y1-m.GetTopMargin(), len(lines))], "\n")
		}
	}
	return ""
}

// TestSidebarAgentPrefixYieldsInOrder pins the ladder a narrowing row walks
// down, on the budget the row draws with. The session goes before the agent
// because the gutter already marks a pane that is somewhere else, and the
// whole prefix goes before a cell of the pane name does. A prefix token that
// does not fit is passed over, so the session comes back when the agent is
// too wide for the row and the session alone is not. The E2E test TestNarrowRailKeepsTheAgentNameBeforeItsHarness
// covers the harness half on screen; the session half is only reached on a
// pane from another session, which that test does not draw.
func TestSidebarAgentPrefixYieldsInOrder(t *testing.T) {
	m := &OS{Settings: config.Global}
	tokens := []sidebarAgentToken{
		{Name: "session", Text: "api"},
		{Name: "harness", Text: sidebarHarnessLabel("claude-code")},
	}
	const nameW = 6 // "deploy"
	for _, tc := range []struct {
		avail int
		want  string
	}{
		{19, "api/claude/"}, // both fit beside the whole name, with room to spare
		{17, "api/claude/"}, // both fit exactly: 11 cells and the 6 of the name
		{16, "claude/"},     // the session yields first
		{13, "claude/"},     // the agent fits exactly beside the whole name
		{12, "api/"},        // the agent no longer fits, and the shorter session does
		{10, "api/"},        // the session fits exactly beside the whole name
		{9, ""},             // and yields before a cell of the name goes
		{6, ""},
		{1, ""},
	} {
		keep, room := railRowFit(nameW, railNameKeep(nameW), sidebarAgentBudget(tokens, nil, "", "", sidebarAgentSep()), tc.avail)
		run := m.sidebarAgentPrefixRun(tokens, keep[:len(tokens)], lipgloss.NewStyle(), theme.UI())
		if got := ansi.Strip(run); got != tc.want {
			t.Errorf("prefix at avail=%d = %q, want %q", tc.avail, got, tc.want)
		}
		if want := tc.avail - lipgloss.Width(tc.want); room != want {
			t.Errorf("prefix at avail=%d leaves the name %d cells, want %d", tc.avail, room, want)
		}
	}
}

// TestRailHarnessIsInTheSignature: a harness the cache cannot see leaves a row
// naming yesterday's agent on screen.
func TestRailHarnessIsInTheSignature(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(tree)
	before := m.sidebarSignature()
	m.Windows[1].AgentHarness = "gemini-cli"
	if m.sidebarSignature() == before {
		t.Error("a window's harness is drawn on its agent row but is not in the rail signature")
	}
}

// TestRailAgentMessageIsInTheSignature: the note line draws it, so a pane that
// says something new and nothing else has to rebuild the rail. Folding what is
// drawn is the whole contract the cache runs on.
func TestRailAgentMessageIsInTheSignature(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(tree)
	before := m.sidebarSignature()
	m.Windows[1].AgentMessage = "running tests"
	if m.sidebarSignature() == before {
		t.Error("a window's agent message is drawn on its note line but is not in the rail signature")
	}
}
