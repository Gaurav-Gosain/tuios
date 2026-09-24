package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// railRowsByKind is every window a rendered rail drew, by the kind of row.
func railRowsByKind(m *OS) map[sidebarRowKind][]string {
	out := map[sidebarRowKind][]string{}
	for _, h := range m.SidebarHits {
		if h.WindowID != "" {
			out[h.Kind] = append(out[h.Kind], h.WindowID)
		}
	}
	return out
}

// TestCompactRailListsEachAgentOnce: on a rail of sidebarCompactWidth or less,
// a pane running an agent is listed once, in the agents section with its note
// line, and the terminals section keeps the panes that run none. At 24
// columns an agent was listed three times: its terminals row, its agents row
// and that row's note line.
func TestCompactRailListsEachAgentOnce(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 40)
	if w := m.GetSidebarWidth(); w > sidebarCompactWidth {
		t.Fatalf("the fixture rail is %d wide; the test needs a compact one", w)
	}
	lines := railPlain(t, m, tree)
	rows := railRowsByKind(m)

	for _, id := range []string{"bbbbbbbb2222", "cccccccc3333"} {
		if strings.Contains(strings.Join(rows[sidebarRowWindow], " "), id) {
			t.Errorf("agent pane %s is still listed in terminals:\n%s", id, strings.Join(lines, "\n"))
		}
		if !strings.Contains(strings.Join(rows[sidebarRowAgent], " "), id) {
			t.Errorf("agent pane %s is not in the agents section:\n%s", id, strings.Join(lines, "\n"))
		}
	}
	if !strings.Contains(strings.Join(rows[sidebarRowWindow], " "), "aaaaaaaa1111") {
		t.Errorf("the plain pane left the terminals section:\n%s", strings.Join(lines, "\n"))
	}
	// The note line still says what the pane is doing.
	if !strings.Contains(strings.Join(lines, "\n"), "editing files") {
		t.Errorf("the agent's note line is gone:\n%s", strings.Join(lines, "\n"))
	}

	// Past the compact width nothing changes: both sections list the pane.
	wideRail(m)
	lines = railPlain(t, m, tree)
	rows = railRowsByKind(m)
	if !strings.Contains(strings.Join(rows[sidebarRowWindow], " "), "cccccccc3333") {
		t.Errorf("a wide rail dropped an agent pane from terminals:\n%s", strings.Join(lines, "\n"))
	}
}

// TestCompactRailFocusMarkFollowsTheAgent: the focused pane's row carries the
// focus mark, and on a compact rail an agent pane's only row is its agents
// row.
func TestCompactRailFocusMarkFollowsTheAgent(t *testing.T) {
	m, _ := sectionsTestOS(t, 120, 40)
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Workspace: 1},
			{ID: "cccccccc3333", Title: "build", AgentState: "working", Focused: true, Workspace: 1},
		}},
	})
	lines := railPlain(t, m, tree)
	focus := m.Settings.GetRailFocusMark()
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowAgent && h.WindowID == "cccccccc3333" {
			if row := lines[h.Y0-m.GetTopMargin()]; !strings.HasPrefix(row, focus) {
				t.Errorf("the focused agent's row %q does not carry the focus mark %q", row, focus)
			}
			return
		}
	}
	t.Fatalf("no agents row for the focused agent:\n%s", strings.Join(lines, "\n"))
}

// TestCompactRailPeekListsEveryPane: a peek is a request to see a session's
// panes, so it lists the agent panes too.
func TestCompactRailPeekListsEveryPane(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 40)
	m.SidebarPeek = "api"
	railPlain(t, m, tree)
	if rows := railRowsByKind(m); !strings.Contains(strings.Join(rows[sidebarRowWindow], " "), "dddddddd4444") {
		t.Errorf("a peek at api left out its agent pane: %v", rows[sidebarRowWindow])
	}
}

// TestRailAgeWaitsUntilItMatters: "<1m" sat on nearly every agent row. A row
// says how long only once the pane has been in its state for railAgeFloor,
// and the row under the cursor says it at any age.
func TestRailAgeWaitsUntilItMatters(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-30 * time.Second).UnixNano()
	old := now.Add(-12 * time.Minute).UnixNano()
	if got := railAgentAge("working", fresh, now); got != "" {
		t.Errorf("a 30s old state shows %q", got)
	}
	if got := railAgentAge("needs_input", old, now); got != "12m" {
		t.Errorf("a 12m old state shows %q, want 12m", got)
	}

	m, _ := sectionsTestOS(t, 120, 40)
	wideRail(m)
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "fresh", AgentState: "working", StateAt: fresh, Workspace: 1},
			{ID: "bbbbbbbb2222", Title: "stale", AgentState: "needs_input", StateAt: old, Workspace: 1},
		}},
	})
	lines := railPlain(t, m, tree)
	if row := railAgentRow(m, lines, "aaaaaaaa1111"); strings.Contains(row, "<1m") {
		t.Errorf("a fresh working row carries an age: %q", row)
	}
	if row := railAgentRow(m, lines, "bbbbbbbb2222"); !strings.Contains(row, "12m") {
		t.Errorf("a row waiting 12m does not say so: %q", row)
	}
	e := sidebarAgentEntry{SessionID: "main", WindowID: "aaaaaaaa1111", Title: "fresh", State: "working", StateAt: fresh}
	lit := stripANSIForTrace(m.sidebarAgentRow(e, sidebarVariantFull, 30, theme.UI(), sidebarRowState{Cursor: true}, false))
	if !strings.Contains(lit, "<1m") {
		t.Errorf("the row under the cursor does not say how long: %q", lit)
	}
}
