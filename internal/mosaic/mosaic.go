// Package mosaic draws a picture with text: each terminal cell becomes one
// block glyph with a foreground and a background colour, the way chafa's
// symbol mode does.
//
// It is what tuios shows for a pane's image when the host terminal draws no
// graphics at all: the Linux console under kmscon, a plain SSH client, a
// terminal without sixel or kitty graphics. No device access is needed and the
// result is ordinary cells, so it scrolls, clips and dims like text.
//
// A cell is split into a grid of sub-pixels (1x2 for half blocks up to 2x4 for
// octants). Each sub-pixel is the average of the image pixels under it, taken
// in linear light. The sub-pixels are then split into the two groups whose
// means, in OKLab, leave the least error, by trying every split: at most 128
// for an octant. The larger the grid, the finer the shapes, at the cost of
// needing a font that has the glyphs.
package mosaic

import (
	"image"
	"image/color"
	"math"
)

// Kind is the set of glyphs a cell may be drawn with.
type Kind uint8

const (
	// Off draws no picture.
	Off Kind = iota
	// Half uses the upper and lower half blocks: 1x2 sub-pixels. Every
	// font with box drawing has them, the Linux console's included.
	Half
	// Quadrant uses the quadrant blocks of U+2580..U+259F: 2x2. They are in
	// the Basic Multilingual Plane, so GNU Unifont and kmscon's built-in
	// font have them.
	Quadrant
	// Sextant uses U+1FB00..U+1FB3B (Unicode 13): 2x3.
	Sextant
	// Octant uses U+1CD00..U+1CDE5 (Unicode 16) and the blocks that complete
	// the set: 2x4.
	Octant
)

// kindNames are the names ParseKind accepts, in Kind order.
var kindNames = [...]string{"off", "half", "quadrant", "sextant", "octant"}

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "off"
}

// ParseKind reads a Kind from its name.
func ParseKind(s string) (Kind, bool) {
	for i, n := range kindNames {
		if n == s {
			return Kind(i), true
		}
	}
	return Off, false
}

// grid is the sub-pixel grid of a kind: gx columns by gy rows.
func (k Kind) grid() (gx, gy int) {
	switch k {
	case Half:
		return 1, 2
	case Quadrant:
		return 2, 2
	case Sextant:
		return 2, 3
	case Octant:
		return 2, 4
	}
	return 0, 0
}

// Cell is one drawn cell: Glyph in Fg on Bg. A cell with Clear set is
// transparent, so the pane's own background shows. A cell whose Bg has alpha
// 0 is drawn on the pane's own background, and so is the space of a cell
// whose Fg has alpha 0.
//
// The colours are values, not interfaces: a picture holds thousands of cells,
// and a boxed colour is a pointer and an allocation each. The glyph is one of
// the strings of a fixed table, so it costs nothing either.
type Cell struct {
	Glyph  string
	Fg, Bg color.RGBA
	// FgIndex and BgIndex are Fg and Bg as ANSI indices, 0 to 15, in cells
	// drawn for ANSI16. The terminal paints those from its own palette, so
	// they are sent as indices; Fg and Bg are the VGA colours they stand for.
	FgIndex, BgIndex uint8
	Clear            bool
}

// Indexed is a paletted picture, the form a decoded sixel image is kept in.
// Pix holds one entry per pixel, row by row: 0 is transparent and n is
// Palette[n-1].
type Indexed struct {
	Width, Height int
	Pix           []uint16
	Palette       []color.RGBA
}

// Colors is the set of colours the host terminal shows.
type Colors uint8

const (
	// TrueColor is 24-bit colour: every colour is sent as it is.
	TrueColor Colors = iota
	// XTerm256 is the xterm 256-colour palette.
	XTerm256
	// ANSI16 is the sixteen ANSI colours, as on the Linux console.
	ANSI16
)

// Encode draws img as rows by cols cells, each cellW by cellH of its pixels,
// with the glyphs of kind k. The result is row by row. Pixels past the image
// are transparent.
//
// At XTerm256 every colour chosen is one of the palette's entries 16 to 255
// (the first 16 are the terminal's own and unknown). The sub-pixels are
// dithered with a 4x4 Bayer matrix before they are snapped to the palette, so
// a gradient that falls between two entries is drawn as a pattern of both
// rather than as bands: two colours in one cell is exactly what a glyph can
// show.
//
// At ANSI16 the cells are always half blocks, whatever k is: see
// encodeANSI16.
func Encode(img Indexed, cellW, cellH, rows, cols int, k Kind, colors Colors) []Cell {
	if k == Off || rows <= 0 || cols <= 0 || cellW <= 0 || cellH <= 0 {
		return nil
	}
	if colors != ANSI16 {
		if gx, _ := k.grid(); gx == 0 {
			return nil
		}
	}
	out := make([]Cell, rows*cols)
	EncodeRegion(out, img, cellW, cellH, cols, image.Rect(0, 0, cols, rows), k, colors)
	return out
}

// EncodeRegion draws the cells of region (in cells: X is the column, Y the
// row) into dst, which holds the whole picture row by row, cols cells a row.
// The cells outside region are left as they are. The dither is ordered, so a
// cell comes out the same whichever region it was drawn in, and a picture can
// be drawn a part at a time, as its parts come into view.
func EncodeRegion(dst []Cell, img Indexed, cellW, cellH, cols int, region image.Rectangle, k Kind, colors Colors) {
	if k == Off || cols <= 0 || cellW <= 0 || cellH <= 0 {
		return
	}
	region = region.Intersect(image.Rect(0, 0, cols, len(dst)/cols))
	if region.Empty() {
		return
	}
	if colors == ANSI16 {
		encodeANSI16(dst, img, cellW, cellH, cols, region)
		return
	}
	xterm256 := colors == XTerm256
	gx, gy := k.grid()
	if gx == 0 {
		return
	}
	n := gx * gy
	// The palette in linear light, once: the average of a sub-pixel is
	// taken there, so a fine pattern of black and white averages to the
	// grey the eye sees from a distance.
	lin := linearPalette(img)
	// Sub-pixel bounds inside a cell, the same for every cell.
	var xs, ys [5]int
	for i := range gx + 1 {
		xs[i] = i * cellW / gx
	}
	for i := range gy + 1 {
		ys[i] = i * cellH / gy
	}
	glyphs := glyphStrings(k)
	var sub [8]sample
	for r := region.Min.Y; r < region.Max.Y; r++ {
		for c := region.Min.X; c < region.Max.X; c++ {
			x0, y0 := c*cellW, r*cellH
			for sy := range gy {
				for sx := range gx {
					sp := average(img, lin,
						x0+xs[sx], y0+ys[sy], x0+xs[sx+1], y0+ys[sy+1])
					if xterm256 && !sp.clear {
						sp.lab = ditherToPalette(sp.lab, c*gx+sx, r*gy+sy)
					}
					sub[sy*gx+sx] = sp
				}
			}
			cell := fit(sub[:n], glyphs)
			if xterm256 {
				cell.Fg, cell.Bg = snap(cell.Fg), snap(cell.Bg)
			}
			dst[r*cols+c] = cell
		}
	}
}

// linearPalette is img's palette in linear light.
func linearPalette(img Indexed) [][3]float32 {
	lin := make([][3]float32, len(img.Palette))
	for i, c := range img.Palette {
		lin[i] = [3]float32{toLinear[c.R], toLinear[c.G], toLinear[c.B]}
	}
	return lin
}

// sample is one sub-pixel: its colour in OKLab (averaged in linear light), and
// whether it is mostly transparent.
type sample struct {
	lab   [3]float32
	clear bool
}

// average is the sub-pixel covering the image pixels in [x0,x1) x [y0,y1).
// A large box is sampled on a lattice of at most 8x8 pixels: a cell is drawn
// from a few dozen pixels, and reading every pixel of a large picture would
// cost the time without changing the glyph.
func average(img Indexed, lin [][3]float32, x0, y0, x1, y1 int) sample {
	stepX := max(1, (x1-x0)/8)
	stepY := max(1, (y1-y0)/8)
	var sum [3]float32
	opaque, total := 0, 0
	for y := y0; y < y1; y += stepY {
		inside := y < img.Height
		for x := x0; x < x1; x += stepX {
			total++
			if !inside || x >= img.Width {
				continue
			}
			v := img.Pix[y*img.Width+x]
			if v == 0 || int(v) > len(lin) {
				continue
			}
			p := lin[v-1]
			sum[0] += p[0]
			sum[1] += p[1]
			sum[2] += p[2]
			opaque++
		}
	}
	if total == 0 || opaque*2 < total {
		return sample{clear: true}
	}
	f := 1 / float32(opaque)
	return sample{lab: linearToOKLab(sum[0]*f, sum[1]*f, sum[2]*f)}
}

// fit splits the sub-pixels into a foreground and a background group and
// picks the glyph whose shape is the foreground group.
func fit(sub []sample, glyphs []string) Cell {
	n := len(sub)
	full := uint32(1)<<n - 1
	var clearMask uint32
	for i, s := range sub {
		if s.clear {
			clearMask |= 1 << i
		}
	}
	if clearMask == full {
		return Cell{Clear: true}
	}
	if clearMask != 0 {
		// The transparent sub-pixels are the background, which is the
		// pane's own, and the rest are one colour.
		fg := mean(sub, full&^clearMask)
		return Cell{Glyph: glyphs[full&^clearMask], Fg: fg}
	}
	// Every split, with the last sub-pixel always in the background: the
	// other half of the splits are these with the colours swapped. The
	// error of a group is sum|x|^2 - |sum x|^2 / count, so only the sums
	// are needed per split.
	var sq float32
	var tot [3]float32
	for _, s := range sub {
		for ch := range 3 {
			tot[ch] += s.lab[ch]
			sq += s.lab[ch] * s.lab[ch]
		}
	}
	best, bestErr := uint32(0), float32(math.MaxFloat32)
	for m := uint32(0); m < 1<<(n-1); m++ {
		var a [3]float32
		na := 0
		for i := range n - 1 {
			if m&(1<<i) != 0 {
				na++
				a[0] += sub[i].lab[0]
				a[1] += sub[i].lab[1]
				a[2] += sub[i].lab[2]
			}
		}
		nb := n - na
		e := sq
		var b [3]float32
		for ch := range 3 {
			b[ch] = tot[ch] - a[ch]
		}
		if na > 0 {
			e -= (a[0]*a[0] + a[1]*a[1] + a[2]*a[2]) / float32(na)
		}
		e -= (b[0]*b[0] + b[1]*b[1] + b[2]*b[2]) / float32(nb)
		// A split has to be better by more than rounding to win, so a
		// flat cell stays a blank and does not become a glyph in one
		// colour on the same colour.
		if e < bestErr-1e-6 {
			best, bestErr = m, e
		}
	}
	bg := mean(sub, full&^best)
	if best == 0 {
		return Cell{Glyph: " ", Bg: bg}
	}
	return Cell{Glyph: glyphs[best], Fg: mean(sub, best), Bg: bg}
}

// mean is the colour of the sub-pixels in mask, averaged in OKLab.
func mean(sub []sample, mask uint32) color.RGBA {
	var s [3]float32
	k := 0
	for i := range sub {
		if mask&(1<<i) != 0 {
			s[0] += sub[i].lab[0]
			s[1] += sub[i].lab[1]
			s[2] += sub[i].lab[2]
			k++
		}
	}
	if k == 0 {
		return color.RGBA{A: 255}
	}
	f := 1 / float32(k)
	return okLabToSRGB(s[0]*f, s[1]*f, s[2]*f)
}

// toLinear maps an sRGB channel to linear light.
var toLinear = func() (t [256]float32) {
	for i := range t {
		v := float64(i) / 255
		if v <= 0.04045 {
			t[i] = float32(v / 12.92)
		} else {
			t[i] = float32(math.Pow((v+0.055)/1.055, 2.4))
		}
	}
	return t
}()

func linearToOKLab(r, g, b float32) [3]float32 {
	l := float64(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := float64(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := float64(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	l, m, s = math.Cbrt(l), math.Cbrt(m), math.Cbrt(s)
	return [3]float32{
		float32(0.2104542553*l + 0.7936177850*m - 0.0040720468*s),
		float32(1.9779984951*l - 2.4285922050*m + 0.4505937099*s),
		float32(0.0259040371*l + 0.7827717662*m - 0.8086757660*s),
	}
}

func okLabToSRGB(L, A, B float32) color.RGBA {
	l := float64(L + 0.3963377774*A + 0.2158037573*B)
	m := float64(L - 0.1055613458*A - 0.0638541728*B)
	s := float64(L - 0.0894841775*A - 1.2914855480*B)
	l, m, s = l*l*l, m*m*m, s*s*s
	r := 4.0767416621*l - 3.3077115913*m + 0.2309699292*s
	g := -1.2684380046*l + 2.6097574011*m - 0.3413193965*s
	b := -0.0041960863*l - 0.7034186147*m + 1.7076147010*s
	return color.RGBA{toSRGB(r), toSRGB(g), toSRGB(b), 255}
}

func toSRGB(v float64) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 1:
		return 255
	case v <= 0.0031308:
		v *= 12.92
	default:
		v = 1.055*math.Pow(v, 1/2.4) - 0.055
	}
	return uint8(v*255 + 0.5)
}
