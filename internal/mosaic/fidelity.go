package mosaic

import (
	"image/color"
	"math"

	"github.com/charmbracelet/x/ansi"
)

// Fidelity says how much of a picture's shape survives in its cells: the
// correlation between the picture's lightness and the cells' lightness, each
// averaged over blocks of 2x2 cells so that a dither pattern counts as the
// shade it makes. 1 is every light and dark area where it belongs, 0 is no
// relation. A transparent pixel and a cell's own background count as black.
//
// A picture with almost no change in lightness has no shape to keep. It
// scores by how close the cells' mean lightness is to the picture's.
func Fidelity(img Indexed, cellW, cellH, rows, cols int, cells []Cell) float64 {
	if rows <= 0 || cols <= 0 || len(cells) < rows*cols {
		return 0
	}
	src := make([]float64, rows*cols)
	shown := make([]float64, rows*cols)
	for r := range rows {
		for c := range cols {
			var sum float64
			n := 0
			for y := r * cellH; y < (r+1)*cellH; y += max(1, cellH/8) {
				for x := c * cellW; x < (c+1)*cellW; x += max(1, cellW/8) {
					n++
					if x >= img.Width || y >= img.Height {
						continue
					}
					if v := img.Pix[y*img.Width+x]; v != 0 && int(v) <= len(img.Palette) {
						sum += lightness(img.Palette[v-1])
					}
				}
			}
			src[r*cols+c] = sum / float64(max(n, 1))
			cell := cells[r*cols+c]
			if cell.Clear {
				continue
			}
			f := coverage(cell.Glyph)
			shown[r*cols+c] = f*colorLightness(cell.Fg) + (1-f)*colorLightness(cell.Bg)
		}
	}
	bs := blocks(src, rows, cols)
	bv := blocks(shown, rows, cols)
	var ms, mv float64
	for i := range bs {
		ms += bs[i]
		mv += bv[i]
	}
	ms /= float64(len(bs))
	mv /= float64(len(bv))
	var sv, ss, vv float64
	for i := range bs {
		a, b := bs[i]-ms, bv[i]-mv
		sv += a * b
		ss += a * a
		vv += b * b
	}
	if math.Sqrt(ss/float64(len(bs))) < 0.02 {
		return max(0, 1-5*math.Abs(ms-mv))
	}
	if vv == 0 {
		return 0
	}
	return sv / math.Sqrt(ss*vv)
}

// blocks averages v over 2x2 windows, sliding one cell at a time. A picture
// one cell high or wide is used as it is.
func blocks(v []float64, rows, cols int) []float64 {
	if rows < 2 || cols < 2 {
		return v
	}
	out := make([]float64, 0, (rows-1)*(cols-1))
	for r := range rows - 1 {
		for c := range cols - 1 {
			out = append(out, (v[r*cols+c]+v[r*cols+c+1]+v[(r+1)*cols+c]+v[(r+1)*cols+c+1])/4)
		}
	}
	return out
}

// lightness is a colour's luma, 0 to 1.
func lightness(c color.RGBA) float64 {
	return (0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)) / 255
}

func colorLightness(c color.Color) float64 {
	switch v := c.(type) {
	case nil:
		return 0
	case ansi.BasicColor:
		return lightness(vga16[int(v)&15])
	case color.RGBA:
		return lightness(v)
	}
	r, g, b, _ := c.RGBA()
	return lightness(color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 255})
}

// coverage is the part of a cell a glyph's foreground covers.
func coverage(glyph string) float64 {
	if f, ok := glyphCoverage[glyph]; ok {
		return f
	}
	return 0
}

var glyphCoverage = func() map[string]float64 {
	m := map[string]float64{}
	for k := Half; k <= Octant; k++ {
		gx, gy := k.grid()
		n := gx * gy
		for mask, g := range glyphStrings(k) {
			bits := 0
			for i := range n {
				if mask&(1<<i) != 0 {
					bits++
				}
			}
			m[g] = float64(bits) / float64(n)
		}
	}
	return m
}()

// MinFidelity is the Fidelity under which a picture drawn at sixteen colours
// is not shown, and the image box is drawn instead.
//
// Measured on ten pictures (photos, wallpapers, textures, a logo) at full
// contrast and at 30, 15, 8 and 4 per cent: half blocks with the Bayer
// dither scored 0.62 at the lowest (fine contour lines at full contrast) and
// above 0.74 everywhere else. Without the dither, the low-contrast ones fell
// to 0.23 to 0.47, where the picture is flat bands with no shape left; that
// is what this cuts off. Random noise scores 0.53 even in 24-bit colour.
const MinFidelity = 0.5
