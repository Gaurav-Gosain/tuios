package shot

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math/rand"
	"testing"
)

// The latency tape for the PNG raster.
//
// Nothing here asserts. The numbers are read by hand against the budget the
// corrective spec measured on the maintainer's machine, which is why the
// canvas size is logged beside them: a render is only slow relative to how
// many pixels it was asked for.

// denseGrid builds a realistic dense capture: styled text, box drawing and a
// scattering of colours, so the benchmark measures the work a real capture
// asks for rather than a blank card.
func denseGrid(cols, rows int) *Grid {
	g := NewGrid(cols, rows, RGB(0xcd, 0xd6, 0xf4), RGB(0x1e, 0x1e, 0x2e))
	rng := rand.New(rand.NewSource(7))
	glyphs := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 ./-_=+()[]{}")
	box := []rune("│─╭╯╰╮├┤┬┴┼█▀▄")
	for y := range rows {
		for x := range cols {
			c := &g.Cells[y][x]
			c.Width = 1
			if x == 0 || x == cols-1 || y == 0 || y == rows-1 {
				c.Cluster = string(box[rng.Intn(len(box))])
			} else {
				c.Cluster = string(glyphs[rng.Intn(len(glyphs))])
			}
			c.FG = RGB(uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)))
			if rng.Intn(6) == 0 {
				c.BG = RGB(uint8(rng.Intn(80)), uint8(rng.Intn(80)), uint8(rng.Intn(80)))
			} else {
				c.BGDefault = true
				c.BG = g.BG
			}
			c.Bold = rng.Intn(8) == 0
		}
	}
	return g
}

func benchFrame(scale int) *Frame {
	return &Frame{
		Mode: FrameWindow, Padding: 48, Radius: 10, Shadow: true,
		Controls: ControlsMacOS, Title: "bench",
		Accent:     RGB(0xcb, 0xa6, 0xf7),
		WashStart:  RGB(0x30, 0x30, 0x50),
		WashEnd:    RGB(0x20, 0x28, 0x40),
		FontFamily: "JetBrains Mono, monospace", Scale: scale,
	}
}

// BenchmarkRenderPNG times a whole capture, from grid to file bytes.
func BenchmarkRenderPNG(b *testing.B) {
	for _, tc := range []struct{ cols, rows int }{{120, 30}, {213, 53}} {
		b.Run(fmt.Sprintf("%dx%d", tc.cols, tc.rows), func(b *testing.B) {
			g, f := denseGrid(tc.cols, tc.rows), benchFrame(2)
			out, err := RenderPNG(g, f, nil)
			if err != nil {
				b.Fatal(err)
			}
			cfg, _, _ := image.DecodeConfig(bytes.NewReader(out))
			b.Logf("canvas %dx%d, %d KB", cfg.Width, cfg.Height, len(out)/1024)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := RenderPNG(g, f, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkRenderPNGPhases splits the same render into the passes the spec
// costed separately, so a regression names itself.
func BenchmarkRenderPNGPhases(b *testing.B) {
	g, f := denseGrid(213, 53), benchFrame(2)
	faces, err := loadFaces(f, pngBaseFontSize*float64(f.Scale))
	if err != nil {
		b.Fatal(err)
	}
	cw, ch := faces.cellSize()
	l := computeLayout(g, f, cw, ch, float64(f.Scale))
	newCanvas := func() *image.RGBA {
		return image.NewRGBA(image.Rect(0, 0, int(l.w+0.5), int(l.h+0.5)))
	}
	b.Logf("canvas %dx%d, cell %.2fx%.2f (ratio %.3f)", int(l.w+0.5), int(l.h+0.5), cw, ch, cw/ch)

	b.Run("wash", func(b *testing.B) {
		img := newCanvas()
		for b.Loop() {
			fillDiagonalGradient(img, f.WashStart, f.WashEnd)
		}
	})
	b.Run("shadow", func(b *testing.B) {
		img := newCanvas()
		for b.Loop() {
			drawCardShadow(img, l, ch)
		}
	})
	b.Run("card", func(b *testing.B) {
		img := newCanvas()
		for b.Loop() {
			fillRoundedRect(img, l.cardX, l.cardY, l.cardW, l.cardH, l.radius, g.BG)
		}
	})
	b.Run("glyphs", func(b *testing.B) {
		img := newCanvas()
		runs := textRuns(g)
		for b.Loop() {
			for _, r := range runs {
				drawPNGRun(img, r, l, faces, g)
			}
		}
	})
	b.Run("encode", func(b *testing.B) {
		img := newCanvas()
		fillDiagonalGradient(img, f.WashStart, f.WashEnd)
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		for b.Loop() {
			var buf bytes.Buffer
			if err := enc.Encode(&buf, img); err != nil {
				b.Fatal(err)
			}
		}
	})
}
