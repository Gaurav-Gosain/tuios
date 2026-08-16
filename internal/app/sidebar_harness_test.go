package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// The rail could show eight agents running and not say which of them was
// Claude. The harness was detected, merged and carried on the live window, and
// stopped one layer short of the wire; these are the claims about what it says
// now that it reaches the row.

// railAgentRow returns the rendered line of the agents-section row for a window,
// or "" when the rail drew none.
func railAgentRow(m *OS, lines []string, windowID string) string {
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowAgent && h.WindowID == windowID {
			return lines[h.Y0-m.GetTopMargin()]
		}
	}
	return ""
}

// TestRailAgentRowSaysWhichAgent walks the three things a harness prefix has to
// get right on a row that already carries a name, a glyph and an elapsed time.
func TestRailAgentRowSaysWhichAgent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		title    string
		harness  string
		want     string
		wantNone string
	}{
		{
			name:    "a named pane says which agent is in it",
			title:   "refactor",
			harness: "claude-code",
			want:    "claude/refactor",
		},
		{
			// The row is drawn from the foreground command for an unnamed pane, so
			// the harness would only repeat it.
			name:     "a pane already named after its agent does not say it twice",
			title:    "claude",
			harness:  "claude-code",
			want:     "claude",
			wantNone: "claude/claude",
		},
		{
			name:    "a harness nothing named leaves the row as it was",
			title:   "refactor",
			harness: "",
			want:    "refactor",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := sectionsTestOS(t, 120, 30)
			tree := sessiontree.Build([]sessiontree.SessionInput{
				{Name: "main", Attached: true, IsCurrent: true, CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
					{ID: "aaaaaaaa1111", Title: "nvim", Focused: true, Workspace: 1},
					{ID: "bbbbbbbb2222", Title: tc.title, AgentState: "working", Harness: tc.harness, Workspace: 1},
				}},
			})
			lines := railPlain(t, m, tree)
			row := railAgentRow(m, lines, "bbbbbbbb2222")
			if row == "" {
				t.Fatalf("no agent row was drawn:\n%s", strings.Join(lines, "\n"))
			}
			if !strings.Contains(row, tc.want) {
				t.Errorf("agent row %q does not say %q", row, tc.want)
			}
			if tc.wantNone != "" && strings.Contains(row, tc.wantNone) {
				t.Errorf("agent row %q says %q twice over", row, tc.wantNone)
			}
		})
	}
}

// TestRailAgentRowCarriesBothPrefixes: a pane elsewhere running an agent has two
// things to say in front of its name, and on a rail with room it says both, in
// the order where-then-what.
func TestRailAgentRowCarriesBothPrefixes(t *testing.T) {
	m, _ := sectionsTestOS(t, 120, 30)
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Focused: true, Workspace: 1},
		}},
		{Name: "api", CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "dddddddd4444", Title: "srv", AgentState: "working", Harness: "codex", Workspace: 1},
		}},
	})
	lines := railPlain(t, m, tree)
	row := railAgentRow(m, lines, "dddddddd4444")
	if !strings.Contains(row, "api/codex/srv") {
		t.Errorf("a foreign agent row read %q, want the session then the agent then the pane", row)
	}
}

// TestSidebarAgentPrefixYieldsInOrder pins the ladder a narrowing row walks
// down. The session goes before the agent because the gutter already marks a
// pane that is somewhere else, and the whole prefix goes before a cell of the
// pane name does.
func TestSidebarAgentPrefixYieldsInOrder(t *testing.T) {
	for _, tc := range []struct {
		avail int
		want  string
	}{
		{19, "api/claude/"}, // both fit, with room to spare
		{13, "api/claude/"}, // both fit exactly: 11 cells and the 2 the name is owed
		{12, "claude/"},     // the session yields first
		{8, ""},             // and the agent yields before the name
		{1, ""},
	} {
		if got := sidebarAgentPrefix("api", "claude-code", "refactor", tc.avail); got != tc.want {
			t.Errorf("prefix at avail=%d = %q, want %q", tc.avail, got, tc.want)
		}
	}
}

// TestSidebarHarnessLabelKeepsTheIdentity: the manifests spell out the product,
// and the rail has room for the agent.
func TestSidebarHarnessLabelKeepsTheIdentity(t *testing.T) {
	for id, want := range map[string]string{
		"claude-code":  "claude",
		"gemini-cli":   "gemini",
		"cursor-agent": "cursor",
		"opencode":     "opencode",
		"codex":        "codex",
		"":             "",
		"CLAUDE-CODE":  "claude",
	} {
		if got := sidebarHarnessLabel(id); got != want {
			t.Errorf("harness label of %q = %q, want %q", id, got, want)
		}
	}
}

// TestRailHarnessIsInTheSignature: a harness the cache cannot see leaves a row
// naming yesterday's agent on screen.
func TestRailHarnessIsInTheSignature(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(tree)
	before := m.sidebarSignature()
	m.Windows[1].AgentHarness = "claude-code"
	if m.sidebarSignature() == before {
		t.Error("a window's harness is drawn on its agent row but is not in the rail signature")
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
