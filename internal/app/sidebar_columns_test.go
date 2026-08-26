package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

// nameColumn is where a row's own text starts, measured from the rail's first
// content column.
func nameColumn(t *testing.T, lines []string, name string) int {
	t.Helper()
	for _, l := range lines {
		if c := rowColumn(ansi.Strip(l), name); c >= 0 {
			return c
		}
	}
	t.Fatalf("no rail row carries %q:\n%s", name, strings.Join(lines, "\n"))
	return -1
}

// rowColumn is the display column name starts at in an already-stripped row, or
// -1. Glyphs are multi-byte, so the byte offset is not the column.
func rowColumn(row, name string) int {
	i := strings.Index(row, name)
	if i < 0 {
		return -1
	}
	return lipgloss.Width(row[:i])
}

// TestSidebarRowsShareOneNameSpine pins the columns the rail's row kinds have
// to agree on: every kind that names something starts that name in the same
// place, so the list reads as one column of text rather than a ragged edge.
func TestSidebarRowsShareOneNameSpine(t *testing.T) {
	m, _ := sidebarMultiSessionOS(t, 120, 40)
	m.DaemonClient = nil
	// The terminals section shows the session sidebarCurrentSessionID names,
	// which is m.SessionName, not whichever tree node happens to be marked
	// IsCurrent: the fixture's current session must carry that name.
	m.SessionName = "attached"
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "attached", Attached: true, IsCurrent: true, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "focused", AgentState: "working", Focused: true},
			{ID: "bbbbbbbb2222", Title: "sibling", AgentState: "needs_input"},
			{ID: "cccccccc3333", Title: "plainwin"},
		}},
		{Name: "elsewhere", WindowCount: 1},
	})

	lines, _ := m.sidebarPanelLinesForTree(tree)
	// The agents section names the same panes as the tree, so compare rows that
	// cannot be confused: a tree-only window, a tree-only session, and the agent
	// row for a pane whose title appears once per section.
	spine := nameColumn(t, lines, "plainwin")
	for _, name := range []string{"elsewhere", "sibling"} {
		if got := nameColumn(t, lines, name); got != spine {
			t.Errorf("%q starts at column %d, want the shared spine %d:\n%s",
				name, got, spine, strings.Join(lines, "\n"))
		}
	}

	// The agents section is pinned to the rail's bottom; find its highest-ranked
	// row (needs_input outranks working, so "sibling" leads) by its recorded hit
	// rather than by a fixed line index, and check it sits on the spine too.
	var agentLine string
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowAgent {
			agentLine = lines[h.Y0-m.GetTopMargin()]
			if h.WindowID != "bbbbbbbb2222" {
				t.Fatalf("first agent row targets %q, want the needs_input pane", h.WindowID)
			}
			break
		}
	}
	if agentLine == "" {
		t.Fatal("no agent row recorded")
	}
	if got := rowColumn(ansi.Strip(agentLine), "sibling"); got != spine {
		t.Errorf("agent row starts at column %d, want %d: %q", got, spine, ansi.Strip(agentLine))
	}
}

// The footer carries the rail's own two controls: the file view on the outer
// corner and the collapse toggle on the pane-facing one. What each must keep is
// agreement between the columns it is drawn on and the columns its hit zone
// claims, since nothing recomputes either.
//
// Negative control: with the zones recorded in construction order rather than
// left to right, the ordering assertion below fails, and so does the fuzz
// oracle's rail-hit-band rule. Both were confirmed red before the sort went in.
func TestSidebarFooterZonesMatchWhatIsDrawn(t *testing.T) {
	m := daemonRailOS(t, 120, 40)
	lines, zones := m.sidebarFooter(sidebarVariantFull, 30, theme.UI(), -1, -1,
		func(sidebarRowKind) bool { return false })
	if len(lines) != 1 {
		t.Fatalf("the footer took %d lines, want one", len(lines))
	}
	if len(zones) != 2 {
		t.Fatalf("footer zones = %+v, want the file view control and the collapse toggle", zones)
	}
	if zones[0].Kind != sidebarRowFiles || zones[1].Kind != sidebarRowCollapse {
		t.Fatalf("footer zones = %+v, want files then collapse", zones)
	}
	if zones[0].X1 > zones[1].X0 {
		t.Errorf("the two controls overlap: %+v", zones)
	}
	row := ansi.Strip(lines[0])
	// The expected columns are read off the drawn row, for each control's own
	// text, never taken from the zones the function under test returned.
	if got := rowColumn(row, "«"); got != zones[1].X0 {
		t.Errorf("the toggle is drawn at column %d but its hit zone starts at %d: %q", got, zones[1].X0, row)
	}
	if got := rowColumn(row, sidebarFilesLabel); got != zones[0].X0 {
		t.Errorf("the files control is drawn at column %d but its hit zone starts at %d: %q", got, zones[0].X0, row)
	}
}
