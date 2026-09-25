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
// down. The session goes before the agent because the gutter already marks a
// pane that is somewhere else, and the whole prefix goes before a cell of the
// pane name does. The E2E test TestNarrowRailKeepsTheAgentNameBeforeItsHarness
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
		{12, ""},            // and yields before a cell of the name goes
		{1, ""},
	} {
		run, w := m.sidebarAgentPrefixRun(tokens, lipgloss.NewStyle(), tc.avail, nameW, theme.UI())
		if got := ansi.Strip(run); got != tc.want {
			t.Errorf("prefix at avail=%d = %q, want %q", tc.avail, got, tc.want)
		}
		if want := lipgloss.Width(tc.want); w != want {
			t.Errorf("prefix at avail=%d reports width %d, want %d", tc.avail, w, want)
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

// TestStripTooltipNamesTheAgent: the collapsed strip has two cells and no room
// for a name, so the harness goes where the strip already puts everything it
// cannot draw.
func TestStripTooltipNamesTheAgent(t *testing.T) {
	label := sidebarTooltipAgentLabel(sidebarAgentEntry{
		Title: "refactor", State: "needs_input", Harness: "claude-code",
	})
	if !strings.Contains(label, "claude") {
		t.Errorf("the strip's agent tooltip read %q and never named the agent", label)
	}
	same := sidebarTooltipAgentLabel(sidebarAgentEntry{
		Title: "codex", State: "working", Harness: "codex",
	})
	if strings.Count(same, "codex") != 1 {
		t.Errorf("the strip's agent tooltip read %q, saying one name twice", same)
	}
}
