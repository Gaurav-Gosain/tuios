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

// delinearize is the inverse of linearize: it puts the sRGB transfer curve
// back on one channel so a colour built in linear light can be spelled.
func delinearize(c float64) float64 {
	if c <= 0.0031308 {
		return c * 12.92
	}
	return 1.055*math.Pow(c, 1/2.4) - 0.055
}

// atLuminance returns c with its channels scaled in linear light until its
// relative luminance is target. Luminance is linear in linear light, so scaling
// every channel by the same factor moves the luminance by that factor and
// leaves the chromaticity where it was: a warm brown stays a warm brown at
// every step of a ramp built from it. A channel that runs past its end is
// clamped, which is the one place the hue can drift, and it happens only when
// the target lies past what the colour can say.
func atLuminance(c color.Color, target float64) color.Color {
	r, g, b, _ := c.RGBA()
	lr, lg, lb := linearize(float64(r)/65535), linearize(float64(g)/65535), linearize(float64(b)/65535)
	if l := 0.2126*lr + 0.7152*lg + 0.0722*lb; l > 0 {
		k := target / l
		lr, lg, lb = lr*k, lg*k, lb*k
	} else {
		// Black has no chromaticity to keep, so the step from it is a grey.
		lr, lg, lb = target, target, target
	}
	channel := func(v float64) uint8 {
		return uint8(math.Round(delinearize(math.Min(math.Max(v, 0), 1)) * 255))
	}
	return color.RGBA{R: channel(lr), G: channel(lg), B: channel(lb), A: 0xFF}
}

// Lighter returns the colour one contrast step above bg: the same chromaticity
// at the luminance that measures ratio:1 against it. A neutral ramp is built
// from one ground with this and Darker, which is what keeps a panel reading as
// raised and a card as inset when the ground moves. Past white the step clamps.
func Lighter(bg color.Color, ratio float64) color.Color {
	return atLuminance(bg, math.Min((relativeLuminance(bg)+0.05)*ratio-0.05, 1))
}

// Darker is Lighter's counterpart: the colour that bg measures ratio:1 against
// from above. Past black the step clamps.
func Darker(bg color.Color, ratio float64) color.Color {
	return atLuminance(bg, math.Max((relativeLuminance(bg)+0.05)/ratio-0.05, 0))
}

// Tone returns an ink that measures ratio:1 on bg, or as close above it as the
// ground allows: the ground's own text end carried toward the ground until it
// measures the ratio. This is how an ink hierarchy is held when the ground it
// was picked against changes: the tiers are their ratios, not their hex values,
// and a light ground gets its tiers in dark ink because ContrastText chooses
// the text end by measurement.
//
// A ground that cannot reach the ratio in either direction, a mid grey say,
// gets the text end itself, which is the most it can do.
func Tone(bg color.Color, ratio float64) color.Color {
	ink := ContrastText(bg)
	if ContrastRatio(ink, bg) <= ratio {
		return ink
	}
	// Bisected for the same reason Structure is, and toward the other side of
	// the target: an ink is held to a floor, so the answer is the least blend
	// that still clears it rather than the most that stays under it.
	lo, hi := 0.0, 1.0
	for range 16 {
		mid := (lo + hi) / 2
		if ContrastRatio(MixColors(bg, ink, mid), bg) >= ratio {
			hi = mid
		} else {
			lo = mid
		}
	}
	return MixColors(bg, ink, hi)
}

// ParseHex reads #rgb or #rrggbb, with or without the leading hash. The short
// form is expanded the way CSS expands it, so #f0a and #ff00aa are the same
// colour. Anything else, surrounding space included, reports false.
func ParseHex(s string) (color.RGBA, bool) {
	if len(s) > 0 && s[0] == '#' {
		s = s[1:]
	}
	var n [6]byte
	switch len(s) {
	case 3:
		for i := range 3 {
			d, ok := unhex(s[i])
			if !ok {
				return color.RGBA{}, false
			}
			n[i*2], n[i*2+1] = d, d
		}
	case 6:
		for i := range 6 {
			d, ok := unhex(s[i])
			if !ok {
				return color.RGBA{}, false
			}
			n[i] = d
		}
	default:
		return color.RGBA{}, false
	}
	return color.RGBA{R: n[0]<<4 | n[1], G: n[2]<<4 | n[3], B: n[4]<<4 | n[5], A: 0xff}, true
}

// unhex decodes one hex digit.
func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// Hex formats c as lowercase #rrggbb. Alpha is dropped.
//
// It takes color.RGBA rather than color.Color so a caller holding an RGBA
// value does not box it to make the call.
func Hex(c color.RGBA) string {
	const digits = "0123456789abcdef"
	out := [7]byte{'#'}
	for i, v := range [3]uint8{c.R, c.G, c.B} {
		out[1+i*2] = digits[v>>4]
		out[2+i*2] = digits[v&0x0f]
	}
	return string(out[:])
}
