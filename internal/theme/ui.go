package theme

import (
	"image/color"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/x/exp/charmtone"
)

// The contrast math lives in the overlay package, which owns the palette these
// ratios are measured on and is meant to stand on its own. These are the names
// the rest of tuios already calls it by.
const (
	// ContrastFloor is the ratio a chrome label has to clear against the
	// ground it is drawn on.
	ContrastFloor = overlay.ContrastFloor
	// MarkFloor is the ratio a non-text mark has to clear: a cap, a glyph, a
	// cursor block.
	MarkFloor = overlay.MarkFloor
	// StructureTarget is what a decorative rule aims at: an edge, a separator,
	// a divider. The one class with no floor under it, and the reason there is
	// no floor is that taking the rule away leaves the layout readable.
	StructureTarget = overlay.StructureTarget
)

// ContrastRatio returns the WCAG 2.1 contrast ratio between two colours.
func ContrastRatio(a, b color.Color) float64 { return overlay.ContrastRatio(a, b) }

// Readable returns c lifted toward the ground's text end until it clears
// ContrastFloor against bg, and c untouched when it already does.
func Readable(c, bg color.Color) color.Color { return overlay.Readable(c, bg) }

// ReadableAt is Readable against a chosen floor.
func ReadableAt(c, bg color.Color, floor float64) color.Color {
	return overlay.ReadableAt(c, bg, floor)
}

// uiCanvas is the chrome ramp's darkest base. It is named rather than only
// spelled inside UI() because RailGround reads it once per drawn row, and
// building the whole palette to reach one constant field means measuring a pill
// foreground's contrast every time.
var uiCanvas = charmtone.Pepper

// railRuleMemo holds the grounds a structure ink has been measured for and the
// answers. The measurement bisects over contrast ratios, and the grounds a
// frame draws rules on are a handful of constants that move only when the theme
// does; without it the dock's separator paid for the whole bisection on every
// frame it composed.
//
// Four entries rather than one because a frame uses more than one ground at a
// time: the strip's resting bands and the one under the pointer are different
// fills, and a single slot would miss on every band between them.
var railRuleMemo struct {
	sync.Mutex
	bg   [4][4]uint32
	ink  [4]color.Color
	next int
}

// RailRuleOn is the structure ink for a rule drawn on a ground its caller
// paints itself, like the collapsed strip's bands.
func RailRuleOn(bg color.Color) color.Color {
	r, g, b, a := bg.RGBA()
	key := [4]uint32{r, g, b, a}

	railRuleMemo.Lock()
	defer railRuleMemo.Unlock()
	for i, held := range railRuleMemo.ink {
		if held != nil && railRuleMemo.bg[i] == key {
			return held
		}
	}
	ink := overlay.Structure(bg)
	railRuleMemo.bg[railRuleMemo.next] = key
	railRuleMemo.ink[railRuleMemo.next] = ink
	railRuleMemo.next = (railRuleMemo.next + 1) % len(railRuleMemo.ink)
	return ink
}

// ContrastText picks a foreground that reads on the given (usually saturated)
// background: near-white on a dark or mid accent, near-black on a light one.
func ContrastText(bg color.Color) color.Color { return overlay.ContrastText(bg) }

// mixColors blends a toward b by t in 0..1.
func mixColors(a, b color.Color, t float64) color.Color { return overlay.MixColors(a, b, t) }

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
		Canvas:   uiCanvas,
		Panel:    charmtone.BBQ,
		Surface:  charmtone.Char,
		RowSel:   charmtone.BBQ,
		Card:     charmtone.Iron,
		Selected: charmtone.Charple,

		Fg:    charmtone.Butter,
		FgDim: charmtone.Smoke,
		// One step up the ramp from Oyster, which was the quiet tier's colour
		// until it was measured: 2.60:1 on the canvas, 1.81:1 on the surface a
		// settings hint is written on, 1.35:1 on a card. Quiet is a tier of the
		// hierarchy, not permission to be unreadable, and every surface that
		// wanted a quiet ink had to work around the old one or lose the label.
		FgMute: charmtone.Squid,

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
