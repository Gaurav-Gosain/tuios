package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// railFrame renders the rail and returns its rows with the styling stripped, so
// an assertion is against what the user sees rather than against a fragment a
// helper built.
func railFrame(t *testing.T, m *OS) []string {
	t.Helper()
	return railText(t, m)
}

// The rail is cached by signature, so a width step that the cache cannot see
// would leave yesterday's rail on screen.
func TestSidebarSignatureCoversTheWidthStep(t *testing.T) {
	m := sidebarTestOS(t, 120, 14, "left")
	prev := m.Settings.SidebarWidth
	m.Settings.SidebarWidth = config.SidebarDefaultWidth
	t.Cleanup(func() { m.Settings.SidebarWidth = prev })

	before := m.sidebarSignature()
	m.SidebarSetCollapsed(true)
	if after := m.sidebarSignature(); after == before {
		t.Error("collapsing the rail did not change its signature; the cache would serve the old width")
	}
}

// A click on the toggle has to move the rail. A hit rect that resolves to the
// control is not the control doing anything, which is how the footer shipped a
// stepper only the keyboard could move.
func TestRailToggleClickCollapsesTheRail(t *testing.T) {
	m := sidebarTestOS(t, 120, 14, "left")
	prev := m.Settings.SidebarWidth
	m.Settings.SidebarWidth = config.SidebarDefaultWidth
	t.Cleanup(func() { m.Settings.SidebarWidth = prev })

	railFrame(t, m)
	var step sidebarRowHit
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowCollapse {
			step = h
		}
	}
	if step.X1 == 0 {
		t.Fatal("the footer drew no toggle to click")
	}

	if !m.SidebarClick(step.X0, step.Y0, false) {
		t.Fatal("the toggle did not consume its own click")
	}
	if got := sidebarVariant(m.GetSidebarWidth()); got != sidebarVariantGlyph {
		t.Errorf("a click on the toggle landed on variant %d, want glyph", got)
	}
}
