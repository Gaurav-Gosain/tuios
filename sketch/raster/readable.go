package raster

import (
	"image"
	"image/color"
	"math"
)

// TextFloor is the contrast, as a WCAG ratio, that Options.Readable holds
// theme-coloured text to against what is behind it.
const TextFloor = 4.5

func luminance(c color.NRGBA) float64 {
	ch := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
}

func ratio(a, b color.NRGBA) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func over(fg, bg color.NRGBA) color.NRGBA {
	a := float64(fg.A) / 255
	m := func(f, b uint8) uint8 { return uint8(math.Round(float64(f)*a + float64(b)*(1-a))) }
	return color.NRGBA{m(fg.R, bg.R), m(fg.G, bg.G), m(fg.B, bg.B), 255}
}

// backdrop is the commonest colour in a box of the image, from a grid of at
// most 16 by 8 samples.
func backdrop(img *image.RGBA, b box) (color.NRGBA, bool) {
	r := image.Rect(int(b.x0), int(b.y0), int(math.Ceil(b.x1)), int(math.Ceil(b.y1))).Intersect(img.Bounds())
	if r.Empty() {
		return color.NRGBA{}, false
	}
	count := map[color.NRGBA]int{}
	best, n := color.NRGBA{}, 0
	sx, sy := max(1, r.Dx()/16), max(1, r.Dy()/8)
	for y := r.Min.Y; y < r.Max.Y; y += sy {
		for x := r.Min.X; x < r.Max.X; x += sx {
			c := img.RGBAAt(x, y)
			k := color.NRGBA{c.R, c.G, c.B, 255}
			count[k]++
			if count[k] > n {
				best, n = k, count[k]
			}
		}
	}
	return best, true
}

// readableText picks the colour for a run of theme-coloured text over what is
// already drawn: its own colour when that clears TextFloor, else the theme's
// text or background colour, else near-black or white.
func (r *renderer) readableText(p path, st state) state {
	c, ok := st.paintColor(st.fill, st.fillOpacity)
	if !ok || st.fill.current || len(p) == 0 {
		return st
	}
	b := emptyBox()
	for _, l := range flatten(p, st.m, 1) {
		for _, q := range l.pts {
			b.add(q)
		}
	}
	if b.empty() {
		return st
	}
	bg, ok := backdrop(r.dst, b)
	if !ok || ratio(over(c, bg), bg) >= TextFloor {
		return st
	}
	best, bestRatio := c, ratio(over(c, bg), bg)
	// The theme's own colours first; plain near-black and white only where
	// neither of those reads, on a fill of middle lightness.
	for _, v := range []string{"var(--_fg)", "var(--_bg)", "#111111", "#ffffff"} {
		if bestRatio >= TextFloor {
			break
		}
		if k, ok := parseColor(r.resolve(v)); ok {
			if q := ratio(k, bg); q > bestRatio {
				best, bestRatio = k, q
			}
		}
	}
	best.A = c.A
	st.fill = paint{c: best}
	return st
}
