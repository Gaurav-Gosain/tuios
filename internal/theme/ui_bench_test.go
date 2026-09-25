package theme

import "testing"

// BenchmarkUIPalette is the chrome palette every overlay, the dock and the rail
// ask for while composing. It is called many times per frame, and each call
// rebuilds the struct and re-derives the pill foreground through a pair of
// contrast-ratio computations.
func BenchmarkUIPalette(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = UI()
	}
}
