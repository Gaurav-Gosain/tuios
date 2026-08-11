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

	// The agents section leads the rail, so its first row is the highest-ranked
	// agent; it has to sit on the spine too.
	if got := rowColumn(ansi.Strip(lines[1]), "sibling"); got != spine {
		t.Errorf("agent row starts at column %d, want %d: %q", got, spine, ansi.Strip(lines[1]))
	}
}

// The new-session control moved to the rail's footer, where it reads as a
// control rather than as a row dressed like the sessions it is not one of. What
// it must keep is agreement between the columns it is drawn on and the columns
// its hit zone claims.
func TestSidebarFooterZonesMatchWhatIsDrawn(t *testing.T) {
	m := daemonRailOS(t, 120, 40)
	lines, zones := m.sidebarFooter(sidebarVariantFull, 30, theme.UI(), true, -1, -1,
		func(sidebarRowKind) bool { return false })
	if len(lines) != 1 {
		t.Fatalf("the full-width footer took %d lines, want both controls on one", len(lines))
	}
	row := ansi.Strip(lines[0])
	for _, z := range zones {
		want := "+ new"
		if z.Kind == sidebarRowCollapse {
			want = "«"
		}
		if got := rowColumn(row, want); got != z.X0 {
			t.Errorf("%q is drawn at column %d but its hit zone starts at %d: %q", want, got, z.X0, row)
		}
	}
}
