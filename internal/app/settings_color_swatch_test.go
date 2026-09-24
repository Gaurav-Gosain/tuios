package app

import "testing"

// TestColorSwatchGlyphFramesTheChip is the regression test for a low-contrast
// swatch drawn as one bar in the middle of the chip. The outline has to open
// with the left eighth block and close with the right one, so its lines sit on
// the outer edges.
func TestColorSwatchGlyphFramesTheChip(t *testing.T) {
	got := []rune(colorSwatchGlyph())
	if len(got) != 2 {
		t.Fatalf("colorSwatchGlyph() = %q, want two cells", string(got))
	}
	if got[0] != '▏' || got[1] != '▕' {
		t.Fatalf("colorSwatchGlyph() = %q, want the left eighth block then the right eighth block", string(got))
	}
}
