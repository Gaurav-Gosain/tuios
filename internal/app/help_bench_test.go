package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// BenchmarkRenderHelpMenu is one frame of the help overlay, which is redrawn on
// every frame while it is open. Before the registry kept its key table, every
// one of those frames walked every binding in every scope four times over to
// list the keys, and that was most of the frame.
func BenchmarkRenderHelpMenu(b *testing.B) {
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	if m.KeybindRegistry == nil {
		m.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())
	}
	m.Width, m.Height = 160, 48
	m.EffectiveWidth, m.EffectiveHeight = 160, 48
	m.ShowHelp = true
	m.HelpCategory = 0
	b.ReportAllocs()
	for b.Loop() {
		_, _ = m.RenderHelpMenu()
	}
}
