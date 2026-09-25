package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestCollapseSurvivesARestart: the collapse is a state the user put the rail
// in, so it belongs beside the order and the width rather than being forgotten
// the moment the client reattaches.
func TestCollapseSurvivesARestart(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m := sidebarTestOS(t, 120, 30, "left")
	m.SidebarSetCollapsed(true)

	restored := &OS{Settings: config.Global}
	restored.loadSidebarState()
	if !restored.SidebarCollapsed {
		t.Error("a collapsed rail came back expanded")
	}

	m.SidebarSetCollapsed(false)
	restored = &OS{Settings: config.Global}
	restored.loadSidebarState()
	if restored.SidebarCollapsed {
		t.Error("an expanded rail came back collapsed")
	}
}

// TestCollapseKeepsTheStoredWidth: the two states are "collapsed" and "the
// width you chose", so collapsing must not spend the width on the way down.
// The old stepper wrote the width itself, which is how a user who stepped down
// twice lost the width they had dragged to.
func TestCollapseKeepsTheStoredWidth(t *testing.T) {
	withSidebar(t, true, "left", 34)
	m := sidebarTestOS(t, 200, 30, "left")
	m.Settings.SidebarWidth = 34

	m.SidebarSetCollapsed(true)
	if m.Settings.SidebarWidth != 34 {
		t.Fatalf("collapsing moved the stored width to %d", m.Settings.SidebarWidth)
	}
	if got := m.GetSidebarWidth(); got != config.SidebarGlyphWidth {
		t.Fatalf("a collapsed rail draws %d columns, want the glyph strip", got)
	}
	m.SidebarSetCollapsed(false)
	if got := m.GetSidebarWidth(); got != 34 {
		t.Errorf("expanding landed on %d columns, want the stored 34", got)
	}
}
