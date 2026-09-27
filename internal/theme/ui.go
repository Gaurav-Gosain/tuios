package theme

import (
	"image/color"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/x/exp/charmtone"
	tint "github.com/lrstanley/bubbletint/v2"
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

// UIPalette is the chrome color set for TUIOS floating overlays. It is an alias
// for overlay.Palette so the overlay package stays free of any tuios
// dependency and could be published on its own.
type UIPalette = overlay.Palette

// chromeRamp is the constant ramp stated as the ratios between its steps,
// which is what a theme that names its own Surface is held to. The four
// neutrals are not independent: they are one ramp at fixed spacing, and that
// spacing is what makes a panel read as raised and a card as inset. The three
// inks are the same kind of thing, a hierarchy stated as ratios on the surface
// they are written on. Measured from the constants rather than typed, so the
// two ways of building a palette cannot drift apart.
//
// Contrast ratios rather than luminance ratios, because contrast is what every
// ink in the chrome is measured by, and because the flare term in it is what
// keeps the steps of a ramp built on a near-black ground apart: in bare
// luminance a third of almost nothing is nothing.
var chromeRamp = struct {
	canvas, panel, card float64 // steps below, below and above Surface
	fg, fgDim, fgMute   float64 // ink tiers on Surface
}{
	canvas: overlay.ContrastRatio(charmtone.Char, charmtone.Pepper),
	panel:  overlay.ContrastRatio(charmtone.Char, charmtone.BBQ),
	card:   overlay.ContrastRatio(charmtone.Iron, charmtone.Char),
	fg:     overlay.ContrastRatio(charmtone.Butter, charmtone.Char),
	fgDim:  overlay.ContrastRatio(charmtone.Smoke, charmtone.Char),
	fgMute: overlay.ContrastRatio(charmtone.Squid, charmtone.Char),
}

// UI returns the active chrome palette. Neutrals and semantic status colors come
// from the charmtone palette so overlays read consistently regardless of the
// terminal theme; the accent follows the active terminal theme when one is
// enabled, falling back to charmtone Charple.
//
// Chrome is intentionally kept on a constant neutral ramp (like a real window
// manager keeps its chrome constant) so overlays stay legible over any terminal
// content, while a themed session still tints its tabs, selection and badges.
// A theme that names a chrome Surface moves the ramp, and the ink tiers move
// with it: every ink was chosen against the ground it is written on, so
// opening the ground up without re-deriving the inks would trade off-accent
// chrome for unreadable chrome. See Chrome.
//
// The palette is built for the colour depth the terminal has (see Depth): the
// designed ramp in truecolor, a hand-set grey ramp at 256 colours, and the
// terminal's own colours, slots and attributes at 16. Every derived token and
// every contrast check goes through overlay.Derive.
//
// It is memoised on what it is built from, because the derivation measures
// every ink on every ground it is promised on and render code asks for the
// palette many times a frame.
func UI() overlay.Palette {
	key := currentUIKey()
	uiMemo.Lock()
	defer uiMemo.Unlock()
	if uiMemo.valid && uiMemo.key == key {
		return uiMemo.pal
	}
	p := buildUI(key.depth)
	uiMemo.key, uiMemo.valid, uiMemo.pal = key, true, p
	return p
}

// uiKey is everything UI reads: the depth, the theme and its chrome. The
// theme's colours are keyed by pointer, and a theme file that is read again
// makes new ones, so an edited theme is a new key.
type uiKey struct {
	depth   overlay.Depth
	tint    *tint.Tint
	colours [6]*tint.Color
	chrome  *Chrome
}

func currentUIKey() uiKey {
	k := uiKey{depth: Depth()}
	if t := Current(); t != nil {
		k.tint = t
		k.colours = [6]*tint.Color{t.BrightBlue, t.BrightCyan, t.BrightRed, t.BrightGreen, t.Yellow, t.Bg}
		k.chrome = CurrentChrome()
	}
	return k
}

var uiMemo struct {
	sync.Mutex
	key   uiKey
	valid bool
	pal   overlay.Palette
}

// buildUI builds the palette for depth d.
func buildUI(d overlay.Depth) overlay.Palette {
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
	customRamp := false

	if t := Current(); t != nil {
		p.Accent = t.BrightBlue
		p.AccentBright = t.BrightCyan
		p.Selected = t.BrightBlue
		p.Warn = t.BrightRed
		p.Success = t.BrightGreen
		p.Info = t.BrightBlue
		p.Warning = t.Yellow

		// A theme that names its own chrome colours overrides the slots above,
		// one role at a time. See Chrome: the point is a palette whose accent
		// is not its bright blue.
		if c := CurrentChrome(); c != nil {
			if c.Accent != nil {
				p.Accent, p.Selected, p.Info = c.Accent, c.Accent, c.Accent
			}
			if c.AccentBright != nil {
				p.AccentBright = c.AccentBright
			}
			if c.Success != nil {
				p.Success = c.Success
			}
			if c.Warning != nil {
				p.Warning = c.Warning
			}
			if c.Error != nil {
				p.Warn = c.Error
			}
			if c.Info != nil {
				p.Info = c.Info
			}
			if c.Canvas != nil {
				p.Canvas, customRamp = c.Canvas, true
			}
			if c.Panel != nil {
				p.Panel, p.RowSel, customRamp = c.Panel, c.Panel, true
			}
			if c.Surface != nil {
				p.Surface, customRamp = c.Surface, true
				p.Fg, p.FgDim, p.FgMute = c.fg, c.fgDim, c.fgMute
			}
			if c.Card != nil {
				p.Card, customRamp = c.Card, true
			}
		}
	}

	switch d {
	case overlay.Depth16:
		// The theme's own sixteen, by slot, so the terminal paints them from
		// the user's palette. With no theme the slots are the same ones a
		// theme's chrome derives from, and the user's terminal is the theme.
		p.Accent, p.Selected, p.Info = overlay.Slot(12), overlay.Slot(12), overlay.Slot(12)
		p.AccentBright = overlay.Slot(14)
		p.Warn, p.Success, p.Warning = overlay.Slot(9), overlay.Slot(10), overlay.Slot(3)
		// The primary and secondary inks are the terminal's own foreground,
		// which is picked to read on its own background; the quiet ink is
		// bright black, the one slot every palette makes a quiet grey.
		p.Fg, p.FgDim, p.FgMute = overlay.NoColor, overlay.NoColor, overlay.Slot(8)
	case overlay.Depth256:
		if !customRamp {
			// Set by hand on the grey ramp. Stepping the charmtone neutrals
			// down one at a time put the panel band and the cursor row on
			// index 17, navy, because BBQ's slight blue cast wins the colour
			// cube by perceptual distance.
			p.Canvas, p.Panel, p.Surface = overlay.Grey(2), overlay.Grey(3), overlay.Grey(5)
			p.RowSel, p.RowSelQuiet, p.Hover, p.Card = overlay.Grey(3), overlay.Grey(4), overlay.Grey(6), overlay.Grey(7)
			p.Fg, p.FgDim, p.FgMute = overlay.Grey(23), overlay.Grey(18), overlay.Grey(13)
		}
		p.Accent, p.AccentBright, p.Selected = overlay.To256(p.Accent), overlay.To256(p.AccentBright), overlay.To256(p.Selected)
		p.Warn, p.Success = overlay.To256(p.Warn), overlay.To256(p.Success)
		p.Info, p.Warning = overlay.To256(p.Info), overlay.To256(p.Warning)
	}

	return overlay.Derive(p)
}

// GroundUI is UI for chrome written straight on the terminal's own
// background rather than on a dialog's surface: the rail and the dock.
//
// UI's neutrals and inks are a dark ramp, which is right for the dialogs that
// paint their own ground and right beside a dark theme. The rail and the dock
// paint nothing, so their labels land on the theme's background, and on a
// light theme the ramp's near-white inks were written on near-white: the
// rail's session names, the dock's notice and the dock's buttons could not be
// read. On a light ground the ramp is rebuilt from that ground, with the
// ground as its canvas and a band one step darker as its surface, and the ink
// tiers are measured on it the way a theme's own chrome surface is. On a dark
// ground this is UI unchanged, and so it is at 16 colours, where the ground is
// the terminal's own whatever it is.
func GroundUI() overlay.Palette {
	p := UI()
	if p.Depth == overlay.Depth16 {
		return p
	}
	ground := RailGround()
	if !groundIsLight(ground) {
		return p
	}
	if c := CurrentChrome(); c != nil && c.Surface != nil {
		// A theme that names its own chrome surface has already chosen the
		// ramp its chrome is drawn in.
		return p
	}
	key := currentUIKey()
	c := groundChrome(ground, p.Depth)
	if c.derived && c.derivedFor == key {
		return c.pal
	}
	p.Canvas, p.Panel, p.RowSel, p.Surface, p.Card = c.Canvas, c.Panel, c.Panel, c.Surface, c.Card
	p.Fg, p.FgDim, p.FgMute = c.fg, c.fgDim, c.fgMute
	p.RowSelQuiet, p.Hover = nil, nil
	p = overlay.Derive(p)
	groundChromeMemo.Lock()
	groundChromeMemo.c.pal, groundChromeMemo.c.derived, groundChromeMemo.c.derivedFor = p, true, key
	groundChromeMemo.Unlock()
	return p
}

// groundIsLight reports whether dark ink reads better on ground than light.
func groundIsLight(ground color.Color) bool {
	return overlay.ContrastRatio(ground, color.Black) > overlay.ContrastRatio(ground, color.White)
}

// groundChromeMemo keeps the last ramp built for a ground: GroundUI is asked
// on every rail and dock rebuild, and the ramp only changes with the theme.
// The palette derived from it is kept beside it, since deriving measures every
// ink on every ground.
var groundChromeMemo struct {
	sync.Mutex
	key   [4]uint32
	depth overlay.Depth
	valid bool
	c     groundRamp
}

// groundRamp is a light ground's ramp, and the palette derived from it once
// GroundUI has asked for one.
type groundRamp struct {
	Chrome
	pal        overlay.Palette
	derived    bool
	derivedFor uiKey
}

// groundChrome is the neutral ramp and ink tiers for chrome on a light ground.
func groundChrome(ground color.Color, d overlay.Depth) groundRamp {
	r, g, b, a := ground.RGBA()
	key := [4]uint32{r, g, b, a}
	groundChromeMemo.Lock()
	defer groundChromeMemo.Unlock()
	if groundChromeMemo.valid && groundChromeMemo.key == key && groundChromeMemo.depth == d {
		return groundChromeMemo.c
	}
	surface := overlay.Darker(ground, chromeRamp.canvas)
	c := Chrome{
		Canvas:  ground,
		Surface: surface,
		Panel:   overlay.Darker(surface, chromeRamp.panel),
	}
	c.deriveRamp()
	groundChromeMemo.key, groundChromeMemo.depth, groundChromeMemo.valid = key, d, true
	groundChromeMemo.c = groundRamp{Chrome: c}
	return groundChromeMemo.c
}
