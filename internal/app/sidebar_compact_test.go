package app

import (
	"strings"
	"testing"
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
