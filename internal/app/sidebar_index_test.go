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
// number switch_session_N opens, and a pending chord changes nothing about
// the rail.
func TestRailRowsWearTheirSwitchNumbers(t *testing.T) {
	withSessionColors(t, true)
	m, tree := sessionColorOS(t, 120, 40)

	idle := railRowFor(t, m, tree, "api")
	if !strings.Contains(idle, "2 api") {
		t.Errorf("the api session's row does not lead with its switch number: %q", idle)
	}

	base := m.sidebarSignature()
	m.PrefixActive = true
	if m.sidebarSignature() != base {
		t.Error("arming the prefix chord changed the rail")
	}
	again := railRowFor(t, m, tree, "api")
	if again != idle {
		t.Errorf("a pending chord redrew the row: %q, want %q", again, idle)
	}
}
