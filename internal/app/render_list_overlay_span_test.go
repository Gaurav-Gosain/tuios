package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestWorkspaceSwitcherRowKeepsItsFacts: a workspace named past the row's budget
// used to be handed to the panel over-wide, and the panel's backstop then cut
// the right-hand end, taking the pane count and the "current" marker with it.
// The label is what gives way now, so the facts on the right always survive.
func TestWorkspaceSwitcherRowKeepsItsFacts(t *testing.T) {
	for _, term := range []int{51, 100, 190} {
		m := newNarrowOS(t, term, 40)
		m.CurrentWorkspace = 1
		m.Windows = []*terminal.Window{
			{ID: "a", CustomName: "one", Width: 80, Height: 24, Workspace: 1},
			{ID: "b", CustomName: "two", Width: 80, Height: 24, Workspace: 2},
		}
		m.WorkspaceNames = map[int]string{
			1: strings.Repeat("long-workspace-name", 3),
			2: "short",
		}
		m.WorkspaceSwitcherItems = m.buildWorkspaceItems()

		content, geo, _ := m.renderWorkspaceSwitcher()
		lines := strings.Split(content, "\n")
		var row string
		for _, ln := range lines {
			if strings.Contains(ln, "long-workspace-name") {
				row = ln
				break
			}
		}
		if row == "" {
			t.Fatalf("term=%d: no named workspace row in:\n%s", term, content)
		}
		if w := lipgloss.Width(row); w != geo.Width {
			t.Errorf("term=%d: row is %d cells, panel is %d", term, w, geo.Width)
		}
		plain := ansiEscape.ReplaceAllString(row, "")
		if !strings.Contains(plain, "current") {
			t.Errorf("term=%d: the current-workspace marker was cut: %q", term, plain)
		}
		if !strings.Contains(plain, "pane") {
			t.Errorf("term=%d: the pane count was cut: %q", term, plain)
		}
		// The panel's own right-hand pad survives, so the row does not run to the
		// panel edge the way an over-wide row does.
		if !strings.HasSuffix(plain, "  ") {
			t.Errorf("term=%d: row runs to the panel edge: %q", term, plain)
		}
	}
}
