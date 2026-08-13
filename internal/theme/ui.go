package theme

import (
	"image/color"
	"math"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/x/exp/charmtone"
)

// ContrastFloor is the ratio a chrome label has to clear against the ground it
// is drawn on. WCAG AA for body text, applied to chrome because chrome is where
// the small type is: the dock's labels are one row tall and read at a glance.
const ContrastFloor = 4.5

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

// mixColors blends a toward b by t in 0..1.
func mixColors(a, b color.Color, t float64) color.Color {
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
// It exists for the colours the theme owns. The accent follows the terminal
// theme, so an accent label on the chrome's own dark ground is legible only for
// the themes that happen to be bright ones; blending toward the text colour
// keeps the hue that says "this is the current thing" and buys the legibility
// with luminance instead.
func Readable(c, bg color.Color) color.Color {
	if ContrastRatio(c, bg) >= ContrastFloor {
		return c
	}
	target := ContrastText(bg)
	// Sixteen steps puts the answer within ~6% of the least blending that
	// works, which is finer than the terminal's own colour rounding.
	const steps = 16
	for i := 1; i < steps; i++ {
		if mixed := mixColors(c, target, float64(i)/steps); ContrastRatio(mixed, bg) >= ContrastFloor {
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

// UIPalette is the chrome color set for TUIOS floating overlays. It is an alias
// for overlay.Palette so the overlay package stays free of any tuios
// dependency and could be published on its own.
type UIPalette = overlay.Palette

// UI returns the active chrome palette. Neutrals and semantic status colors come
// from the charmtone palette so overlays read consistently regardless of the
// terminal theme; the accent follows the active terminal theme when one is
// enabled, falling back to charmtone Charple.
//
// Chrome is intentionally kept on a constant neutral ramp (like a real window
// manager keeps its chrome constant) so overlays stay legible over any terminal
// content, while a themed session still tints its tabs, selection and badges.
func UI() overlay.Palette {
	p := overlay.Palette{
		Canvas:   charmtone.Pepper,
		Panel:    charmtone.BBQ,
		Surface:  charmtone.Char,
		RowSel:   charmtone.BBQ,
		Card:     charmtone.Iron,
		Selected: charmtone.Charple,

		Fg:     charmtone.Butter,
		FgDim:  charmtone.Smoke,
		FgMute: charmtone.Oyster,

		Accent:       charmtone.Charple,
		AccentBright: charmtone.Hazy,
		PillFg:       charmtone.Pepper,

		Warn:    charmtone.Cherry,
		Success: charmtone.Julep,
		Info:    charmtone.Malibu,
		Warning: charmtone.Tang,
	}

	if t := Current(); t != nil {
		p.Accent = t.BrightBlue
		p.AccentBright = t.BrightCyan
		p.Selected = t.BrightBlue
		p.Warn = t.BrightRed
		p.Success = t.BrightGreen
		p.Info = t.BrightBlue
		p.Warning = t.Yellow
	}

	// Pick the pill foreground for contrast against whichever accent is active.
	p.PillFg = ContrastText(p.Accent)

	return p
}
