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

// MarkFloor is the ratio a non-text mark has to clear: a cap, a glyph, a
// cursor block. WCAG holds graphical objects to 3:1 rather than 4.5:1 because
// a shape survives what small type does not.
const MarkFloor = 3.0

// StructureTarget is what decorative structure aims at: an edge rule, a
// separator, a group divider. It is the third class beside the two floors
// above, and the rule the three of them state together is that chrome goes
// quieter only where WCAG never put a floor. 1.4.11 exempts a purely decorative
// separator from the 3:1 non-text floor, and a separator is the one piece of
// chrome that carries no meaning of its own: take the rule away and the layout
// still reads off alignment and whitespace. Nothing that is text or a mark is
// allowed down here.
//
// A target rather than a floor, because structure fails by being loud at least
// as often as by being faint. Drawn in the same ink as the labels it frames, a
// rail's edge plus a dock's separator is more cells than every label in the
// frame put together, which is what a rail looks like when it looks busy.
const StructureTarget = 1.9

// Structure returns the ink a decorative rule is drawn in on a given ground:
// the ground's own text end carried back toward the ground until it measures
// about StructureTarget against it.
//
// Measured against the ground rather than fixed, because no one neutral is
// quiet at both ends. The quietest an ink can be against black and white at the
// same time is 4.58:1, louder than the labels a rule is meant to sit under, so
// a fixed choice buys its quiet on one ground by drawing a hard line on the
// other: a dark grey that whispers on Canvas measures 8.44:1 on white.
func Structure(bg color.Color) color.Color {
	ink := ContrastText(bg)
	if ContrastRatio(ink, bg) <= StructureTarget {
		return ink
	}
	// Bisected rather than scanned, because the ratio falls unevenly along the
	// blend: near a very dark ground one step of it moves the ratio further than
	// ten steps do near a pale one, so an even scan overshot the target by six
	// percent on the darkest themes and by one on the rest. The ratio decreases
	// with the blend, so halving the interval sixteen times pins the least blend
	// that reaches the target to finer than the eight bits a colour has to say
	// it in.
	lo, hi := 0.0, 1.0
	for range 16 {
		mid := (lo + hi) / 2
		if ContrastRatio(MixColors(ink, bg, mid), bg) > StructureTarget {
			lo = mid
		} else {
			hi = mid
		}
	}
	return MixColors(ink, bg, hi)
}

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
