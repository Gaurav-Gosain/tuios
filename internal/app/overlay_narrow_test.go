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
