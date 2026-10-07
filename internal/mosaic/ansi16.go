package mosaic

import (
	"image/color"
	"math"
	"sync"

	"github.com/charmbracelet/x/ansi"
)

// The sixteen-colour floor.
//
// The Linux console (TERM=linux) shows sixteen colours from a font of 256 or
// 512 glyphs, which carries the CP437 block set and nothing finer. So a cell
// is two sub-pixels, top and bottom, drawn with ▀ or ▄, each dithered to the
// sixteen colours with the 4x4 Bayer matrix.
//
// The colours are sent as ANSI indices, so the terminal paints its own
// palette. The choice is made against the Linux console's default palette,
// the VGA colours, which is also what most terminals ship. Only the first
// eight are used as a background: the Linux console gives the bright
// backgrounds to blink unless told otherwise, so a cell whose two colours are
// both bright gives one of them up for its nearest dark colour.

// vga16 is the Linux console's default palette.
var vga16 = [16]color.RGBA{
	{0, 0, 0, 255}, {170, 0, 0, 255}, {0, 170, 0, 255}, {170, 85, 0, 255},
	{0, 0, 170, 255}, {170, 0, 170, 255}, {0, 170, 170, 255}, {170, 170, 170, 255},
	{85, 85, 85, 255}, {255, 85, 85, 255}, {85, 255, 85, 255}, {255, 255, 85, 255},
	{85, 85, 255, 255}, {255, 85, 255, 255}, {85, 255, 255, 255}, {255, 255, 255, 255},
}

// ansiColors are the indices as colours, boxed once.
var ansiColors = func() (c [16]color.Color) {
	for i := range c {
		c[i] = ansi.BasicColor(i)
	}
	return c
}()

var (
	ansiOnce    sync.Once
	ansiLUT     []uint8
	ansiLab     [16][3]float32
	darkNearest [16]uint8   // the nearest of 0..7 to each colour
	darkCost    [16]float32 // and how far it is
)

func ansiTable() []uint8 {
	ansiOnce.Do(func() {
		for i, c := range vga16 {
			ansiLab[i] = linearToOKLab(toLinear[c.R], toLinear[c.G], toLinear[c.B])
		}
		dist := func(a, b [3]float32) float32 {
			d0, d1, d2 := a[0]-b[0], a[1]-b[1], a[2]-b[2]
			return d0*d0 + d1*d1 + d2*d2
		}
		for i := range 16 {
			best, bestD := 0, float32(math.MaxFloat32)
			for j := range 8 {
				if d := dist(ansiLab[i], ansiLab[j]); d < bestD {
					best, bestD = j, d
				}
			}
			darkNearest[i], darkCost[i] = uint8(best), bestD
		}
		ansiLUT = make([]uint8, 1<<15)
		for i := range ansiLUT {
			r, g, b := uint8(i>>10)<<3|4, uint8(i>>5&31)<<3|4, uint8(i&31)<<3|4
			lab := linearToOKLab(toLinear[r], toLinear[g], toLinear[b])
			best, bestD := 0, float32(math.MaxFloat32)
			for j := range 16 {
				if d := dist(lab, ansiLab[j]); d < bestD {
					best, bestD = j, d
				}
			}
			ansiLUT[i] = uint8(best)
		}
	})
	return ansiLUT
}

// ansiStep is the spread of the dither in sRGB units: the gap between the
// VGA palette's levels (0, 85, 170, 255).
const ansiStep = 85

// dither16 is the palette index a sub-pixel at (x, y) becomes.
func dither16(lab [3]float32, x, y int) int {
	c := okLabToSRGB(lab[0], lab[1], lab[2])
	off := ((bayer4[y&3][x&3]+0.5)/16 - 0.5) * ansiStep
	shift := func(v uint8) int { return int(max(0, min(255, float32(v)+off))) >> 3 }
	return int(ansiTable()[shift(c.R)<<10|shift(c.G)<<5|shift(c.B)])
}

// encodeANSI16 draws img as half blocks in the sixteen colours.
func encodeANSI16(img Indexed, cellW, cellH, rows, cols int) []Cell {
	lin := linearPalette(img)
	ansiTable()
	half := cellH / 2
	out := make([]Cell, rows*cols)
	for r := range rows {
		for c := range cols {
			x0, y0 := c*cellW, r*cellH
			top := average(img, lin, x0, y0, x0+cellW, y0+half)
			bot := average(img, lin, x0, y0+half, x0+cellW, y0+cellH)
			var t, b int
			if !top.clear {
				t = dither16(top.lab, c, 2*r)
			}
			if !bot.clear {
				b = dither16(bot.lab, c, 2*r+1)
			}
			out[r*cols+c] = halfCell16(top.clear, bot.clear, t, b)
		}
	}
	return out
}

// halfCell16 is the cell for a top and a bottom colour, either of which may
// be transparent, with a dark background.
func halfCell16(topClear, botClear bool, t, b int) Cell {
	switch {
	case topClear && botClear:
		return Cell{Clear: true}
	case topClear:
		return Cell{Glyph: "▄", Fg: ansiColors[b]}
	case botClear:
		return Cell{Glyph: "▀", Fg: ansiColors[t]}
	case t == b && t < 8:
		return Cell{Glyph: " ", Bg: ansiColors[t]}
	case t == b:
		return Cell{Glyph: "█", Fg: ansiColors[t]}
	case b < 8:
		return Cell{Glyph: "▀", Fg: ansiColors[t], Bg: ansiColors[b]}
	case t < 8:
		return Cell{Glyph: "▄", Fg: ansiColors[b], Bg: ansiColors[t]}
	}
	// Both bright: the one that loses least becomes its dark neighbour.
	if darkCost[b] <= darkCost[t] {
		return Cell{Glyph: "▀", Fg: ansiColors[t], Bg: ansiColors[darkNearest[b]]}
	}
	return Cell{Glyph: "▄", Fg: ansiColors[b], Bg: ansiColors[darkNearest[t]]}
}
