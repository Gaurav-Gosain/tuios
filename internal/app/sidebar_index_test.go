package app

import (
	"strings"
	"testing"

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

// TestRailRowsWearTheirSwitchNumbers: a session row leads with the muted
// number switch_session_N opens, but only while the switch chord is armed. The
// idle rail is the quiet one-machine rail the negative control promises, so the
// number is the chord's answer, drawn when the chord asks.
func TestRailRowsWearTheirSwitchNumbers(t *testing.T) {
	withSessionColors(t, true)
	m, tree := sessionColorOS(t, 120, 40)

	idle := railRowFor(t, m, tree, "api")
	if strings.Contains(idle, "2 api") {
		t.Errorf("the idle rail shows the switch number the chord has not asked for: %q", idle)
	}

	m.PrefixActive = true
	armed := railRowFor(t, m, tree, "api")
	if !strings.Contains(armed, "2 api") {
		t.Errorf("the armed rail does not lead the api session with its switch number: %q", armed)
	}
}
