package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/charmbracelet/x/ansi"
)

// railRowFor finds the drawn row naming a session, stripped of styling.
func railRowFor(t *testing.T, m *OS, tree sessiontree.Tree, name string) string {
	t.Helper()
	lines, _ := m.sidebarPanelLinesForTree(tree)
	for _, l := range lines {
		if plain := ansi.Strip(l); strings.Contains(plain, name) {
			return plain
		}
	}
	t.Fatalf("the rail has no row naming %q", name)
	return ""
}

// TestRailRowsWearTheirSwitchNumbers: with show_numbers on, a session row
// leads with the muted number switch_session_N opens.
func TestRailRowsWearTheirSwitchNumbers(t *testing.T) {
	withSessionColors(t, true)
	withSidebarNumbers(t)
	m, tree := sessionColorOS(t, 120, 40)

	row := railRowFor(t, m, tree, "api")
	if !strings.Contains(row, "2 api") {
		t.Errorf("the rail does not lead the api session with its switch number: %q", row)
	}
}

// TestRailRowsStayQuietWithoutShowNumbers: the numbers are opt-in, and the
// default rail carries only the names.
func TestRailRowsStayQuietWithoutShowNumbers(t *testing.T) {
	withSessionColors(t, true)
	m, tree := sessionColorOS(t, 120, 40)

	row := railRowFor(t, m, tree, "api")
	if strings.Contains(row, "2 api") {
		t.Errorf("the default rail shows a switch number with show_numbers off: %q", row)
	}
}

// withSidebarNumbers turns show_numbers on for one test and puts it back.
func withSidebarNumbers(t *testing.T) {
	t.Helper()
	prev := config.Global.SidebarShowNumbers
	config.Global.SidebarShowNumbers = true
	t.Cleanup(func() { config.Global.SidebarShowNumbers = prev })
}
