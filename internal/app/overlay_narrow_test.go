package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// narrowScreens are the sizes the overlays have to survive: a tall narrow
// terminal, a short wide one, the narrowest viewport worth supporting, and a
// normal terminal as a control.
var narrowScreens = []struct {
	name string
	w, h int
}{
	{"tall-narrow", 51, 37},
	{"short-wide", 90, 20},
	{"very-narrow", 30, 24},
	{"very-short", 100, 12},
	{"desktop", 120, 40},
	// The accent picker's wide layout: the first screen that gets it, one just
	// over it, and a wide screen too short to keep everything.
	{"wide-picker-floor", 73, 30},
	{"wide-picker", 74, 20},
	{"wide-picker-short", 100, 14},
}

// assertFitsScreen fails if any line of out is wider than w, the block is
// taller than h, or any line pads itself with a control character. An overlay
// wider than the screen has its right-hand side drawn off the edge, where it
// cannot be read or scrolled to.
//
// The control-character half guards a different way of getting the same answer
// wrong. Every row here is padded to the panel width with literal spaces, and
// the fitting arithmetic in overlay_fit.go measures those rows to decide what
// else fits. A tab would break that: it is one byte that lipgloss measures as
// one cell but a terminal advances to its next tab stop, so the panel's own
// idea of where a row ends would stop matching the screen's. Carriage returns
// and the rest are the same failure with a different glyph.
func assertFitsScreen(t *testing.T, name, out string, w, h int) {
	t.Helper()
	if out == "" {
		return
	}
	lines := strings.Split(out, "\n")
	for i, ln := range lines {
		if j := strings.IndexAny(ln, "\t\r\v\f"); j >= 0 {
			t.Errorf("%s: line %d pads with a control character (%q at byte %d): %q",
				name, i, ln[j], j, ln)
			return
		}
		if lw := lipgloss.Width(ln); lw > w {
			t.Errorf("%s: line %d is %d cells wide, screen is %d: %q", name, i, lw, w, ln)
			return
		}
	}
	if len(lines) > h {
		t.Errorf("%s: %d lines tall, screen is %d", name, len(lines), h)
	}
}

// newNarrowOS builds an OS sized to a given screen with every overlay's state
// populated enough to render.
func newNarrowOS(t *testing.T, w, h int) *OS {
	t.Helper()
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	if m.KeybindRegistry == nil {
		m.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())
	}
	m.Width, m.Height = w, h
	m.EffectiveWidth, m.EffectiveHeight = w, h
	return m
}
