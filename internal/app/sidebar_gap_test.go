package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestRailKeepsAGapBeforeEveryRightHandFigure is the collision the audit caught
// at width 24: "documentation-site2", a session name fused with its window
// count, and an agent name fused with its elapsed time. The name field was
// measured to the row's edge rather than to the figure sitting at that edge, so
// at the exact width where truncation began the two ran together and the last
// letter of the name read as part of the number.
//
// The gap is not negotiable: what shortens is the name.
func TestRailKeepsAGapBeforeEveryRightHandFigure(t *testing.T) {
	// Every width from the narrowest the rail draws at up past the point the
	// names stop needing truncation, so the boundary case cannot be missed.
	for w := config.SidebarNarrowWidth; w <= 34; w++ {
		names := []string{
			"documentation-site",
			"documentation-sit",
			"documentation-si",
			"a",
			strings.Repeat("x", 40),
		}
		for _, name := range names {
			m := railGapOS(t, name)
			withSidebar(t, true, "left", w)
			m.Settings = config.Global
			tree := sessiontree.Build([]sessiontree.SessionInput{
				{Name: "main", Attached: true, IsCurrent: true, Windows: []sessiontree.WindowInput{
					{ID: "aaaaaaaa1111", Title: name, AgentState: "needs_input", Focused: true},
				}},
				{Name: name, WindowCount: 2},
			})
			lines, cw := m.sidebarPanelLinesForTree(tree)
			for _, line := range lines {
				row := ansi.Strip(line)
				assertFigureHasItsGap(t, row, cw, w, name)
			}
		}
	}
}

// assertFigureHasItsGap checks the cell in front of a row's trailing figure is
// blank. The figure is right-aligned one cell in from the rail's edge, so the
// check reads the row from its right-hand end.
func assertFigureHasItsGap(t *testing.T, row string, cw, width int, name string) {
	t.Helper()
	// Past the rail's own edge rule, which every row ends on.
	trimmed := strings.TrimRight(row, " │|")
	if trimmed == "" {
		return
	}
	// A row ending in a figure ends in digits, or in a duration like "18m": a
	// unit letter is only part of one when digits come before it.
	tail := []rune(trimmed)
	i := len(tail) - 1
	if strings.ContainsRune("smhd", tail[i]) {
		i--
	}
	if i < 0 || !isDigit(tail[i]) {
		return
	}
	for i > 0 && isDigit(tail[i-1]) {
		i--
	}
	if i == 0 {
		return
	}
	if tail[i-1] != ' ' {
		t.Errorf("rail w=%d name=%q: %q has no gap before its figure %q",
			width, name, string(tail), string(tail[i:]))
	}
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// railGapOS is a session whose pane carries the given name, at a rail width the
// caller sets.
func railGapOS(t *testing.T, name string) *OS {
	t.Helper()
	m := newNarrowOS(t, 120, 40)
	m.CurrentWorkspace = 1
	m.SessionName = "main"
	m.Windows = []*terminal.Window{
		{ID: "aaaaaaaa1111", CustomName: name, Width: 40, Height: 20, Workspace: 1, AgentState: "needs_input"},
	}
	m.FocusedWindow = 0
	m.SidebarOrder = nil
	return m
}

// TestRailNameShrinksBeforeTheGapDoes pins which side yields: the available
// name width accounts for the figure, its inset and the gap, so a name is cut
// one cell shorter rather than the gap being spent.
func TestRailNameShrinksBeforeTheGapDoes(t *testing.T) {
	const cw = 24
	for _, rightW := range []int{0, 1, 2, 3} {
		avail := sidebarNameAvail(cw, rightW)
		// The gutter and the glyph are what put the name at its spine column;
		// composing without them would measure a row the rail never draws.
		gutter, glyph := "|", strings.Repeat("g", sidebarNameCol-2)
		row := sidebarComposeRow(gutter, glyph, strings.Repeat("n", avail), strings.Repeat("9", rightW), cw, nil)
		plain := ansi.Strip(row)
		if lipgloss.Width(plain) > cw {
			t.Errorf("rightW=%d: the row is %d cells in a %d-cell rail", rightW, lipgloss.Width(plain), cw)
		}
		if rightW == 0 {
			continue
		}
		trimmed := strings.TrimRight(plain, " ")
		if !strings.HasSuffix(trimmed, strings.Repeat("9", rightW)) {
			t.Fatalf("rightW=%d: the figure is not at the row's end: %q", rightW, plain)
		}
		before := strings.TrimSuffix(trimmed, strings.Repeat("9", rightW))
		if !strings.HasSuffix(before, " ") {
			t.Errorf("rightW=%d: a full-length name left no gap before the figure: %q", rightW, plain)
		}
	}
}
