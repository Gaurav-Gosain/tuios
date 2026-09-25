package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

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
