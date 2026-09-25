package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
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

// TestDockSeparatorFollowsTheTheme: the hairline is cached styled, so its
// colour is part of what the cache is keyed on. A theme switch at an unchanged
// width and glyph has to redraw it in the new theme's rule colour.
//
// Negative control: dropping the colour from the cache key in renderDockString
// fails this with the old theme's SGR still on the row.
func TestDockSeparatorFollowsTheTheme(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	m := newNarrowOS(t, 80, 24)

	hairlineSGR := func() string {
		return sgrForeground(theme.RailRule())
	}
	before := hairlineSGR()
	dock, _ := m.renderDockString()
	if !strings.Contains(dock, before) {
		t.Fatalf("setup: the dock hairline is not drawn in the rule colour %q: %q", before, dock)
	}

	withTheme(t, "nord")
	after := hairlineSGR()
	if after == before {
		t.Fatal("setup: the two themes share a rule colour, so this proves nothing")
	}
	dock, _ = m.renderDockString()
	if !strings.Contains(dock, after) {
		t.Errorf("the dock hairline did not take the new theme's rule colour %q: %q", after, dock)
	}
}
