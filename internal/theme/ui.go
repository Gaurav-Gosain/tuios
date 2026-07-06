package theme

import (
	"image/color"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/x/exp/charmtone"
)

// perceivedLuminance returns the 0..1 perceived brightness of c.
func perceivedLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 65535.0
}

// contrastText picks a foreground that reads on the given (usually saturated)
// background: near-white on a dark/mid accent, near-black on a light one. This
// keeps title chips and active tabs legible regardless of the theme's accent.
func contrastText(bg color.Color) color.Color {
	if perceivedLuminance(bg) < 0.6 {
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
	p.PillFg = contrastText(p.Accent)

	return p
}
