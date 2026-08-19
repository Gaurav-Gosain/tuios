package overlay

import (
	"image/color"
	"math"

	"github.com/charmbracelet/x/exp/charmtone"
)

// ContrastFloor is the ratio a chrome label has to clear against the ground it
// is drawn on. WCAG AA for body text, applied to chrome because chrome is where
// the small type is: the dock's labels are one row tall and read at a glance.
const ContrastFloor = 4.5

// MarkFloor is the ratio a non-text mark has to clear: a cap, a glyph, a rule,
// a cursor block. WCAG holds graphical objects to 3:1 rather than 4.5:1 because
// a shape survives what small type does not.
const MarkFloor = 3.0

// linearize undoes the sRGB transfer curve for one channel, which is what makes
// the luminance below additive.
func linearize(c float64) float64 {
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// relativeLuminance is the WCAG 2.1 quantity rather than a cheap weighted
// average of the channels. The two disagree by enough to matter at the low end,
// which is exactly where a dock on a dark ground lives.
func relativeLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return 0.2126*linearize(float64(r)/65535) +
		0.7152*linearize(float64(g)/65535) +
		0.0722*linearize(float64(b)/65535)
}

// ContrastRatio returns the WCAG 2.1 contrast ratio between two colours: 1 for
// a pair that are the same, 21 for black against white. Chrome foregrounds are
// picked and tested against this rather than by eye, so a theme swap cannot
// quietly take a label below the floor.
func ContrastRatio(a, b color.Color) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// MixColors blends a toward b by t in 0..1.
func MixColors(a, b color.Color, t float64) color.Color {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	blend := func(x, y uint32) uint8 {
		return uint8((float64(x)*(1-t) + float64(y)*t) / 257)
	}
	return color.RGBA{R: blend(ar, br), G: blend(ag, bg), B: blend(ab, bb), A: 0xFF}
}

// Readable returns c lifted toward the ground's text end until it clears
// ContrastFloor against bg, and c untouched when it already does.
//
// It exists for the colours a theme owns. An accent follows the terminal theme,
// so an accent label on the chrome's own dark ground is legible only for the
// themes that happen to be bright ones; blending toward the text colour keeps
// the hue that says "this is the current thing" and buys the legibility with
// luminance instead.
func Readable(c, bg color.Color) color.Color { return ReadableAt(c, bg, ContrastFloor) }

// ReadableAt is Readable against a chosen floor. A mark that carries its
// meaning in its shape as well as its colour, a cap or a glyph or a rule, is
// held to MarkFloor instead, which keeps more of the hue: lifting a severity
// colour all the way to text contrast is what turns a theme's red into pink.
func ReadableAt(c, bg color.Color, floor float64) color.Color {
	if ContrastRatio(c, bg) >= floor {
		return c
	}
	target := ContrastText(bg)
	// Sixteen steps puts the answer within ~6% of the least blending that
	// works, which is finer than the terminal's own colour rounding.
	const steps = 16
	for i := 1; i < steps; i++ {
		if mixed := MixColors(c, target, float64(i)/steps); ContrastRatio(mixed, bg) >= floor {
			return mixed
		}
	}
	return target
}

// ContrastText picks a foreground that reads on the given (usually saturated)
// background: near-white on a dark/mid accent, near-black on a light one. This
// keeps title chips and active tabs legible regardless of the theme's accent.
//
// The choice is by measured ratio rather than by a luminance threshold. A
// threshold has to be set somewhere, and saturated greens sit right in the miss
// zone: a pure green reads 0.55 perceived against a 0.6 cut and so was given the
// light ink, which measures 1.32:1 on it. Taking whichever ink measures better
// has no miss zone to fall into.
func ContrastText(bg color.Color) color.Color {
	if ContrastRatio(charmtone.Butter, bg) >= ContrastRatio(charmtone.Pepper, bg) {
		return charmtone.Butter
	}
	return charmtone.Pepper
}
