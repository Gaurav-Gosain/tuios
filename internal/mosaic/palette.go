package mosaic

import (
	"image/color"
	"sync"
)

// The xterm 256-colour palette past the first 16: a 6x6x6 cube and a ramp of
// 24 greys. These are the values xterm and the colour profile tuios renders
// with agree on, so a colour picked here reaches the host as its own index.
var xtermPalette = func() (p [240]color.RGBA) {
	levels := [6]uint8{0, 95, 135, 175, 215, 255}
	i := 0
	for r := range 6 {
		for g := range 6 {
			for b := range 6 {
				p[i] = color.RGBA{levels[r], levels[g], levels[b], 255}
				i++
			}
		}
	}
	for k := range 24 {
		v := uint8(8 + 10*k)
		p[i] = color.RGBA{v, v, v, 255}
		i++
	}
	return p
}()

// nearest maps an sRGB colour, 5 bits a channel, to the palette entry closest
// to it in OKLab. Built on first use: 32768 entries.
var (
	nearestOnce sync.Once
	nearestLUT  []uint8
	paletteLab  [240][3]float32
)

func nearestTable() []uint8 {
	nearestOnce.Do(func() {
		for i, c := range xtermPalette {
			paletteLab[i] = linearToOKLab(toLinear[c.R], toLinear[c.G], toLinear[c.B])
		}
		nearestLUT = make([]uint8, 1<<15)
		for i := range nearestLUT {
			r, g, b := uint8(i>>10)<<3|4, uint8(i>>5&31)<<3|4, uint8(i&31)<<3|4
			lab := linearToOKLab(toLinear[r], toLinear[g], toLinear[b])
			best, bestD := 0, float32(1e9)
			for j, p := range paletteLab {
				d0, d1, d2 := lab[0]-p[0], lab[1]-p[1], lab[2]-p[2]
				if d := d0*d0 + d1*d1 + d2*d2; d < bestD {
					best, bestD = j, d
				}
			}
			nearestLUT[i] = uint8(best)
		}
	})
	return nearestLUT
}

func nearestIndex(c color.RGBA) int {
	return int(nearestTable()[int(c.R>>3)<<10|int(c.G>>3)<<5|int(c.B>>3)])
}

// bayer4 is the 4x4 ordered dither matrix, as offsets in (-0.5, 0.5).
var bayer4 = [4][4]float32{
	{0, 8, 2, 10}, {12, 4, 14, 6}, {3, 11, 1, 9}, {15, 7, 13, 5},
}

// ditherStep is the spread of the dither in sRGB units: about the gap between
// two levels of the palette's cube, so a colour between two levels comes out
// as a mix of both in the right proportion.
const ditherStep = 40

// ditherToPalette moves a sub-pixel's colour by the Bayer offset of its
// position and snaps it to the palette. It returns the entry's OKLab.
func ditherToPalette(lab [3]float32, x, y int) [3]float32 {
	c := okLabToSRGB(lab[0], lab[1], lab[2])
	off := ((bayer4[y&3][x&3]+0.5)/16 - 0.5) * ditherStep
	shift := func(v uint8) uint8 {
		return uint8(max(0, min(255, float32(v)+off)))
	}
	c = color.RGBA{shift(c.R), shift(c.G), shift(c.B), 255}
	return paletteLab[nearestIndex(c)]
}

// snap replaces a chosen colour with its nearest palette entry. A group of
// sub-pixels that all hold one entry averages back to that entry, up to
// rounding, so this undoes the rounding.
func snap(c color.Color) color.Color {
	rgba, ok := c.(color.RGBA)
	if !ok {
		return c
	}
	return xtermPalette[nearestIndex(rgba)]
}
