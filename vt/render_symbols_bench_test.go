package vt

import "testing"

// BenchmarkRenderSymbolScreen measures Render on a full screen of box
// drawing, block element, braille and Powerline cells in palette colours,
// the screen a system monitor or a themed prompt leaves behind. Render runs
// for every unfocused pane that changed, and on such a screen nearly every
// neighbouring pair of cells is two non-ASCII symbols.
func BenchmarkRenderSymbolScreen(b *testing.B) {
	e := NewEmulator(symbolScreenCols, symbolScreenRows)
	e.WriteString(symbolScreen())
	b.ReportAllocs()
	for b.Loop() {
		_ = e.Render()
	}
}
