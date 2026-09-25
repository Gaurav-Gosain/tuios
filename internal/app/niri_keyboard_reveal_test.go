package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The scrolling layout has two rules for where the strip sits after focus
// moves, and which one applies is about what the user did rather than about
// what happened.
//
// A focus the user did not ask for, such as a workspace switch restoring its
// own focus, gets the least-scroll rule: a column already on screen does not
// move the strip under them. A key that says "take me to the next pane" is the
// other kind, and it was going through the least-scroll rule too, so cycling
// landed on panes still half off the edge.

// scrollingOS is a client in the scrolling layout with count panes, each wide
// enough that the strip is longer than the view.
func scrollingOS(t *testing.T, count int) *OS {
	t.Helper()
	m := sidebarTestOS(t, 120, 40, "left")
	m.AutoTiling = true
	m.UseScrollingLayout = true
	m.CurrentWorkspace = 1
	for i := range count {
		m.Windows = append(m.Windows, &terminal.Window{
			ID: string(rune('a' + i)), Width: 80, Height: 30, Workspace: 1,
		})
	}
	m.FocusedWindow = 0
	m.TileAllWindows()
	return m
}
