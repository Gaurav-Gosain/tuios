package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestDockSeparatorFollowsItsGlyph: the hairline cache was keyed on width
// alone, so a change of separator character at an unchanged width kept serving
// the old glyph until the next resize.
func TestDockSeparatorFollowsItsGlyph(t *testing.T) {
	m := newNarrowOS(t, 80, 24)

	dock, _ := m.renderDockString()
	if !strings.Contains(dock, config.WindowSeparatorChar) {
		t.Fatalf("the dock hairline is not the separator character: %q", dock)
	}

	prev := m.Settings.UseASCIIOnly
	m.Settings.UseASCIIOnly = true
	t.Cleanup(func() { m.Settings.UseASCIIOnly = prev })

	dock, _ = m.renderDockString()
	if strings.Contains(dock, config.WindowSeparatorChar) {
		t.Errorf("the dock is still drawing the stale hairline glyph: %q", dock)
	}
	if !strings.Contains(dock, strings.Repeat(config.WindowSeparatorCharASCII, 10)) {
		t.Errorf("the dock hairline did not follow the ASCII separator: %q", dock)
	}
}
