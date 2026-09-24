package app

import (
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestColorSwatchIsOnlyFill is the regression test for a low-contrast swatch
// drawn with an outline glyph, which read as a bar through the chip. A swatch
// is two painted cells with no glyph, whatever the contrast with its ground.
func TestColorSwatchIsOnlyFill(t *testing.T) {
	ground := color.RGBA{0x1e, 0x1e, 0x2e, 0xff}
	for _, c := range []color.Color{
		color.RGBA{0x1e, 0x1e, 0x2e, 0xff}, // the ground itself
		color.RGBA{0x11, 0x11, 0x1b, 0xff}, // close to it
		color.RGBA{0xcb, 0xa6, 0xf7, 0xff}, // far from it
	} {
		got := ansi.Strip(colorSwatch(c, ground))
		if got != "  " || strings.ContainsAny(got, "▏▕[]") {
			t.Errorf("colorSwatch(%v) prints %q, want two blank painted cells", c, got)
		}
	}
}
