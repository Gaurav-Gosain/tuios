package overlay

import (
	"image/color"
	"math"
)

// Blending is done in OKLab, a perceptual space: equal steps of a blend look
// like equal steps, and a red tint and a green tint mixed into the same ground
// at the same fraction come out at the same perceived lightness. Blending the
// gamma-encoded sRGB values, which is what this package did before, darkens
// the middle of every blend and makes a saturated tint muddy.
//
// The conversions run per style run in the dim and spotlight passes, so both
// transfer curves are tables: decoding is exact for the 256 channel values,
// and encoding interpolates a 4096-entry table, which is within a quarter of
// one 8-bit step everywhere. Nothing here allocates.

// srgbToLinear decodes one 8-bit channel.
var srgbToLinear [256]float64

// linearToSRGBTable encodes linear light at 4096 even steps from 0 to 1.
var linearToSRGBTable [linearSteps + 1]float64

const linearSteps = 4096

func init() {
	for i := range srgbToLinear {
		srgbToLinear[i] = linearize(float64(i) / 255)
	}
	for i := range linearToSRGBTable {
		linearToSRGBTable[i] = delinearize(float64(i) / linearSteps)
	}
}

// encodeChannel turns linear light into an 8-bit channel, rounded.
func encodeChannel(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	x := v * linearSteps
	i := int(x)
	f := x - float64(i)
	s := linearToSRGBTable[i]*(1-f) + linearToSRGBTable[min(i+1, linearSteps)]*f
	return uint8(math.Round(s * 255))
}

// lab is a colour in OKLab.
type lab struct{ l, a, b float64 }

// toLab converts c to OKLab.
func toLab(c color.Color) lab {
	r16, g16, b16, _ := c.RGBA()
	r, g, b := srgbToLinear[r16>>8], srgbToLinear[g16>>8], srgbToLinear[b16>>8]
	l := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	return lab{
		l: 0.2104542553*l + 0.7936177850*m - 0.0040720468*s,
		a: 1.9779984951*l - 2.4285922050*m + 0.4505937099*s,
		b: 0.0259040371*l + 0.7827717662*m - 0.8086757660*s,
	}
}

// rgba converts back to 8-bit sRGB, clamping a channel that falls outside the
// gamut rather than wrapping it.
func (c lab) rgba() color.RGBA {
	l := c.l + 0.3963377774*c.a + 0.2158037573*c.b
	m := c.l - 0.1055613458*c.a - 0.0638541728*c.b
	s := c.l - 0.0894841775*c.a - 1.2914855480*c.b
	l, m, s = l*l*l, m*m*m, s*s*s
	return color.RGBA{
		R: encodeChannel(+4.0767416621*l - 3.3077115913*m + 0.2309699292*s),
		G: encodeChannel(-1.2684380046*l + 2.6097574011*m - 0.3413193965*s),
		B: encodeChannel(-0.0041960863*l - 0.7034186147*m + 1.7076147010*s),
		A: 0xFF,
	}
}

// okChroma is c's chroma in OKLab: 0 for a grey, about 0.3 for the most
// saturated sRGB colours.
func okChroma(c color.Color) float64 {
	v := toLab(c)
	return math.Hypot(v.a, v.b)
}

// MixColors blends a toward b by t in 0..1, in OKLab. t at 0 is a, t at 1 is
// b, and the result is rounded to the nearest 8-bit colour.
func MixColors(a, b color.Color, t float64) color.Color {
	return mixLab(a, b, t)
}

// mixLab is MixColors with its concrete type, for callers that keep the
// result in a table.
func mixLab(a, b color.Color, t float64) color.RGBA {
	if t <= 0 {
		return toRGBA8(a)
	}
	if t >= 1 {
		return toRGBA8(b)
	}
	x, y := toLab(a), toLab(b)
	return lab{
		l: x.l + (y.l-x.l)*t,
		a: x.a + (y.a-x.a)*t,
		b: x.b + (y.b-x.b)*t,
	}.rgba()
}

// toRGBA8 flattens c to opaque 8-bit channels.
func toRGBA8(c color.Color) color.RGBA {
	r, g, b, _ := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xFF}
}
