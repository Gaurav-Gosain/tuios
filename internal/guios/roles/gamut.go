package roles

// The gamut maths of MyGo's internal/gamut (MIT, Copyright (c) 2026 MyGo
// contributors), so that Oklch here gives the colour ui.Oklch gives: the
// colour is mapped into Display P3 and then to the nearest sRGB colour, as
// CSS Color 4 maps a colour into a gamut.

import "math"

// space is an RGB gamut colors are mapped into.
type space int

const (
	srgb space = iota
	displayP3
)

// decode returns the linear light of an sRGB-encoded component, which may be
// outside 0 to 1.
func decode(v float64) float64 {
	a := math.Abs(v)
	if a <= 0.04045 {
		return v / 12.92
	}
	return math.Copysign(math.Pow((a+0.055)/1.055, 2.4), v)
}

// encode is the inverse of Decode.
func encode(v float64) float64 {
	a := math.Abs(v)
	if a <= 0.0031308 {
		return v * 12.92
	}
	return math.Copysign(1.055*math.Pow(a, 1/2.4)-0.055, v)
}

// oklab converts linear sRGB to Oklab.
func oklab(r, g, b float64) (l, a, bb float64) {
	lc := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	mc := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	sc := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	return 0.2104542553*lc + 0.7936177850*mc - 0.0040720468*sc,
		1.9779984951*lc - 2.4285922050*mc + 0.4505937099*sc,
		0.0259040371*lc + 0.7827717662*mc - 0.8086757660*sc
}

// linearSRGB converts Oklab to linear sRGB, unclamped.
func linearSRGB(l, a, b float64) (r, g, bb float64) {
	lc := l + 0.3963377774*a + 0.2158037573*b
	mc := l - 0.1055613458*a - 0.0638541728*b
	sc := l - 0.0894841775*a - 1.2914855480*b
	lc, mc, sc = lc*lc*lc, mc*mc*mc, sc*sc*sc
	return 4.0767416621*lc - 3.3077115913*mc + 0.2309699292*sc,
		-1.2684380046*lc + 2.6097574011*mc - 0.3413193965*sc,
		-0.0041960863*lc - 0.7034186147*mc + 1.7076147010*sc
}

// linearSRGBToP3 converts linear sRGB to linear Display P3.
func linearSRGBToP3(r, g, b float64) (float64, float64, float64) {
	return 0.8224621*r + 0.1775380*g,
		0.0331941*r + 0.9668058*g,
		0.0170827*r + 0.0723974*g + 0.9105199*b
}

// linearP3ToSRGB converts linear Display P3 to linear sRGB.
func linearP3ToSRGB(r, g, b float64) (float64, float64, float64) {
	return 1.2249401*r - 0.2249404*g,
		-0.0420569*r + 1.0420571*g,
		-0.0196376*r - 0.0786361*g + 1.0982735*b
}

// oklchOf converts Oklab to Oklch, with the hue in degrees.
func oklchOf(l, a, b float64) (float64, float64, float64) {
	h := math.Atan2(b, a) * 180 / math.Pi
	if h < 0 {
		h += 360
	}
	return l, math.Hypot(a, b), h
}

// fromOklch converts Oklch, with the hue in degrees, to Oklab.
func fromOklch(l, c, h float64) (float64, float64, float64) {
	s, co := math.Sincos(h * math.Pi / 180)
	return l, c * co, c * s
}

// toSpace converts an Oklab color to linear RGB of the space.
func toSpace(sp space, l, a, b float64) (float64, float64, float64) {
	r, g, bb := linearSRGB(l, a, b)
	if sp == displayP3 {
		return linearSRGBToP3(r, g, bb)
	}
	return r, g, bb
}

// fromSpace converts linear RGB of the space to Oklab.
func fromSpace(sp space, r, g, b float64) (float64, float64, float64) {
	if sp == displayP3 {
		r, g, b = linearP3ToSRGB(r, g, b)
	}
	return oklab(r, g, b)
}

const (
	// jnd is the Oklab distance CSS Color 4 treats as imperceptible.
	jnd = 0.02
	// epsilon ends the search for the chroma, and absorbs the rounding of
	// the matrices when testing for the gamut.
	epsilon = 0.0001
	inGamut = 0.00001
)

func within(r, g, b float64) bool {
	in := func(v float64) bool { return v >= -inGamut && v <= 1+inGamut }
	return in(r) && in(g) && in(b)
}

func clip(v float64) float64 { return max(0, min(v, 1)) }

// maxChroma is beyond the chroma of any color a display shows: Map starts
// its search there for a larger one, as an infinite one.
const maxChroma = 1

// gamutMap returns the Oklch color as linear RGB inside the gamut of sp. It
// reduces the chroma, keeping lightness and hue, until clipping what is left
// out changes the color imperceptibly (the algorithm of CSS Color 4,
// "gamut mapping to a destination color space"). A negative or NaN chroma
// is 0, as CSS clamps it, a NaN lightness 0 and a hue that is not finite 0.
func gamutMap(sp space, l, c, h float64) (r, g, b float64) {
	if l >= 1 {
		return 1, 1, 1
	}
	if !(l > 0) {
		return 0, 0, 0
	}
	if !(c > 0) {
		c = 0
	}
	c = min(c, maxChroma)
	if math.IsNaN(h) || math.IsInf(h, 0) {
		h = 0
	}
	_, oa, ob := fromOklch(l, c, h)
	r, g, b = toSpace(sp, l, oa, ob)
	if within(r, g, b) {
		return clip(r), clip(g), clip(b)
	}
	// distance is the Oklab distance between the clipped color and lab.
	clipped := func(a, bb float64) (cr, cg, cb, e float64) {
		r, g, b := toSpace(sp, l, a, bb)
		cr, cg, cb = clip(r), clip(g), clip(b)
		cl, ca, cbb := fromSpace(sp, cr, cg, cb)
		return cr, cg, cb, math.Sqrt((cl-l)*(cl-l) + (ca-a)*(ca-a) + (cbb-bb)*(cbb-bb))
	}
	cr, cg, cb, e := clipped(oa, ob)
	if e < jnd {
		return cr, cg, cb
	}
	lo, hi := 0.0, c
	loIn := true
	for hi-lo > epsilon {
		mid := (lo + hi) / 2
		_, a, bb := fromOklch(l, mid, h)
		r, g, b := toSpace(sp, l, a, bb)
		if loIn && within(r, g, b) {
			lo = mid
			continue
		}
		cr, cg, cb, e = clipped(a, bb)
		if e < jnd {
			if jnd-e < epsilon {
				return cr, cg, cb
			}
			loIn = false
			lo = mid
		} else {
			hi = mid
		}
	}
	return cr, cg, cb
}
