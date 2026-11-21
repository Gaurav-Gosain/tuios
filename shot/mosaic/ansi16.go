package mosaic

import (
	"image"
	"image/color"
	"math"
	"sync"
)

var vga16 = [16]color.RGBA{
	{0, 0, 0, 255}, {170, 0, 0, 255}, {0, 170, 0, 255}, {170, 85, 0, 255},
	{0, 0, 170, 255}, {170, 0, 170, 255}, {0, 170, 170, 255}, {170, 170, 170, 255},
	{85, 85, 85, 255}, {255, 85, 85, 255}, {85, 255, 85, 255}, {255, 255, 85, 255},
	{85, 85, 255, 255}, {255, 85, 255, 255}, {85, 255, 255, 255}, {255, 255, 255, 255},
}

var (
	ansiOnce    sync.Once
	ansiLUT     []uint8
	ansiLab     [16][3]float32
	darkNearest [16]uint8
	darkCost    [16]float32
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

const ansiStep = 85

func dither16(lab [3]float32, x, y int) int {
	c := okLabToSRGB(lab[0], lab[1], lab[2])
	off := ((bayer4[y&3][x&3]+0.5)/16 - 0.5) * ansiStep
	shift := func(v uint8) int { return int(max(0, min(255, float32(v)+off))) >> 3 }
	return int(ansiTable()[shift(c.R)<<10|shift(c.G)<<5|shift(c.B)])
}

func encodeANSI16(dst []Cell, img Indexed, lin [][3]float32, cellW, cellH, cols int, region image.Rectangle) {
	ansiTable()
	half := cellH / 2
	for r := region.Min.Y; r < region.Max.Y; r++ {
		for c := region.Min.X; c < region.Max.X; c++ {
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
			dst[r*cols+c] = halfCell16(top.clear, bot.clear, t, b)
		}
	}
}

func cell16(glyph string, fg, bg int) Cell {
	c := Cell{Glyph: glyph}
	if fg >= 0 {
		c.Fg, c.FgIndex = vga16[fg], uint8(fg)
	}
	if bg >= 0 {
		c.Bg, c.BgIndex = vga16[bg], uint8(bg)
	}
	return c
}

func halfCell16(topClear, botClear bool, t, b int) Cell {
	switch {
	case topClear && botClear:
		return Cell{Clear: true}
	case topClear:
		return cell16("▄", b, -1)
	case botClear:
		return cell16("▀", t, -1)
	case t == b && t < 8:
		return cell16(" ", -1, t)
	case t == b:
		return cell16("█", t, -1)
	case b < 8:
		return cell16("▀", t, b)
	case t < 8:
		return cell16("▄", b, t)
	}
	if darkCost[b] <= darkCost[t] {
		return cell16("▀", t, int(darkNearest[b]))
	}
	return cell16("▄", b, int(darkNearest[t]))
}
