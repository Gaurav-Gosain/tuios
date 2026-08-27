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

// TestCollapseFoldsThroughTheBreakpoints: collapse is a preferred width like
// any other, so the responsive clamps still have the last word. A rail
// collapsed on a wide screen stays collapsed when the screen narrows, and a
// screen that pins the rail to the strip by itself does not silently record a
// collapse the user never asked for.
func TestCollapseFoldsThroughTheBreakpoints(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)

	wide := sidebarTestOS(t, 120, 30, "left")
	wide.SidebarCollapsed = true
	if got := wide.GetSidebarWidth(); got != config.SidebarGlyphWidth {
		t.Errorf("collapsed on a 120-column screen draws %d columns", got)
	}

	// 90 columns is past the full breakpoint, so an expanded rail is wide there.
	mid := sidebarTestOS(t, 90, 30, "left")

	wide.Settings = config.Global

	wide.Settings = mid.Settings
	if got := mid.GetSidebarWidth(); got <= config.SidebarNarrowWidth {
		t.Fatalf("precondition: an expanded rail on a 90-column screen draws %d", got)
	}
	mid.SidebarCollapsed = true
	if got := mid.GetSidebarWidth(); got != config.SidebarGlyphWidth {
		t.Errorf("collapsed on a 90-column screen draws %d columns, want the strip", got)
	}

	// The clamp's own narrowing is not a collapse: nothing was stored, so
	// widening the screen brings the rail back without the user doing anything.
	clamped := sidebarTestOS(t, config.SidebarBreakpointNarrow-1, 30, "left")
	mid.Settings = config.Global
	mid.Settings = clamped.Settings
	if clamped.SidebarCollapsed {
		t.Error("a screen too narrow for a wide rail recorded a collapse of its own")
	}
}

// TestExpandingSaysSoWhenTheScreenCannotHonourIt: a control that appears to do
// nothing is worse than one that says why, and the footer already hides its
// arrow in this case, so the key is the path that reaches it.
func TestExpandingSaysSoWhenTheScreenCannotHonourIt(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m := sidebarTestOS(t, config.SidebarBreakpointNarrow-1, 30, "left")
	m.SidebarCollapsed = true

	m.SidebarSetCollapsed(false)
	if !m.SidebarCollapsed {
		t.Error("expanding on a screen with no room for it changed the state anyway")
	}
	if len(m.Notifications) == 0 {
		t.Error("expanding did nothing and said nothing")
	}
}
