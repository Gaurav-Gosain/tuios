package mosaic

import (
	"fmt"
	"image"
	"image/color"
	"testing"
)

// BenchmarkEncode draws an 800x480 picture (80x24 cells of 10x20) with each
// glyph set. It runs once per image, on the pane's PTY reader.
func BenchmarkEncode(b *testing.B) {
	const w, h = 800, 480
	img := Indexed{Width: w, Height: h, Pix: make([]uint16, w*h), Palette: make([]color.RGBA, 256)}
	for i := range img.Palette {
		img.Palette[i] = color.RGBA{uint8(i), uint8(i * 7), uint8(255 - i), 255}
	}
	for y := range h {
		for x := range w {
			img.Pix[y*w+x] = uint16((x*x+y*3)%256 + 1)
		}
	}
	for _, k := range []Kind{Half, Quadrant, Sextant, Octant} {
		for _, c := range []Colors{TrueColor, XTerm256} {
			b.Run(fmt.Sprintf("%s-%s", k, map[Colors]string{TrueColor: "truecolor", XTerm256: "256"}[c]), func(b *testing.B) {
				for b.Loop() {
					Encode(img, 10, 20, 24, 80, k, c)
				}
			})
		}
	}
	b.Run("half-16", func(b *testing.B) {
		for b.Loop() {
			cells := Encode(img, 10, 20, 24, 80, Half, ANSI16)
			Fidelity(img, 10, 20, 80, image.Rect(0, 0, 80, 24), cells)
		}
	})
}
