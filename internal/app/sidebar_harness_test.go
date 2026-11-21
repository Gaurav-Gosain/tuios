package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

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
// whole prefix goes before a cell of the pane name does. Once the agent goes
// the session goes with it, even where the session alone would fit:
// "api/deploy" reads as a harness called api. The E2E test
// TestNarrowRailKeepsTheAgentNameBeforeItsHarness
// covers the harness half on screen; the session half is only reached on a
// pane from another session, which that test does not draw.
func TestSidebarAgentPrefixYieldsInOrder(t *testing.T) {
	m := &OS{Settings: config.Global}
	tokens := []sidebarAgentToken{
		{Name: "session", Text: "api"},
		{Name: "harness", Text: sidebarHarnessLabel("claude-code")},
	}
	const nameW = 6
	for _, tc := range []struct {
		avail int
		want  string
	}{
		{19, "api/claude/"},
		{17, "api/claude/"},
		{16, "claude/"},
		{13, "claude/"},
		{12, ""},
		{10, ""},
		{6, ""},
		{1, ""},
	} {
		keep, room := railRowFit(nameW, railNameKeep(nameW), sidebarAgentBudget(nil, tokens, nil, "", "", sidebarAgentSep()), tc.avail)
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
