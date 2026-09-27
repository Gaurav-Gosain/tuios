// Package overlay provides composable, framework-agnostic building blocks for
// borderless floating overlay panels rendered with charm.land/lipgloss/v2.
//
// The design is deliberately borderless: a panel is a solid Surface-filled
// rectangle whose neutrals step by luminance (Canvas < Panel < Surface < Card)
// so it reads as a raised, floating surface without box-drawing characters.
// Selection is shown with a full-width highlight bar rather than an arrow, and
// an inset accent title chip identifies the panel. At 16 colours, where a
// surface cannot be told from the terminal's own background, a panel draws a
// hairline frame instead and a selected row is reverse video (see Depth).
//
// Every renderer returns both the rendered string and a Geometry describing the
// panel-relative rectangles of its interactive regions (title bar, tabs, body
// origin), so a host can hit-test mouse events without duplicating layout math.
//
// This package is also where tuios keeps its colour tokens: the Palette, the
// rules that derive and contrast-check it (Derive), the per-depth rules
// (depth.go), the perceptual blend (oklab.go) and the one focus and hover rule
// every list follows (rows.go). Render code takes its colours from here and
// from the theme package, never from a literal; a lint holds it to that.
//
// The package holds no tuios state and depends only on lipgloss, the charm x
// packages and the standard library, so it can be lifted out into a standalone
// module.
package overlay

import (
	"image/color"

	"github.com/charmbracelet/x/ansi"
)

// Palette is the semantic color set a panel is rendered with. Callers provide
// it, keeping this package independent of any particular theming system.
//
// The neutral ramp (Canvas, Panel, Surface, RowSel, Card) should step by
// luminance so surfaces read as layered without borders. Accent carries "this
// is the interactive thing"; Warn is reserved for destructive actions.
//
// The fields after Warning are derived: Derive fills them from the ones above,
// so a palette built by hand sets the base tokens and calls Derive once.
type Palette struct {
	Canvas   color.Color // darkest base
	Panel    color.Color // outer band / muted panel base
	Surface  color.Color // the floating panel fill
	RowSel   color.Color // the cursor row of a list that has the keyboard
	Card     color.Color // inset chip / input background
	Selected color.Color // strong selection tint

	Fg     color.Color // primary text
	FgDim  color.Color // secondary / hint text
	FgMute color.Color // tertiary / separators / disabled

	Accent       color.Color // interactive / brand
	AccentBright color.Color // brighter accent for icons/keys
	PillFg       color.Color // foreground that reads on saturated accent pills

	Warn    color.Color // destructive / reset
	Success color.Color // on / enabled
	Info    color.Color // informational
	Warning color.Color // caution

	// RowSelQuiet is the cursor row of a list that does not have the keyboard.
	// It stays visible, so the user can see where the cursor will be when the
	// list gets focus back, and it is quieter than RowSel, so only one list on
	// screen claims the keyboard.
	RowSelQuiet color.Color
	// Hover is the row under the pointer: a light wash over Surface.
	Hover color.Color
	// Edge is the ink of decorative structure on Surface: a panel's frame at
	// 16 colours, a divider. It aims at StructureTarget, not a floor.
	Edge color.Color

	// The status tints are grounds for a pill or a badge: the status colour
	// carried most of the way into Surface, so a badge reads as belonging to
	// its status without shouting. Write on one with Readable(status, tint).
	AccentTint  color.Color
	SuccessTint color.Color
	WarningTint color.Color
	WarnTint    color.Color

	// ScrollThumb and ScrollTrack draw a scrollbar on Surface.
	ScrollThumb color.Color
	ScrollTrack color.Color

	// Depth is the colour depth the palette was built for.
	Depth Depth
	// Framed says a panel draws a hairline frame round itself, because its
	// Surface is not a colour the terminal can tell from what is around it.
	Framed bool
}

// Derive fills p's derived tokens from its base tokens at the current depth
// and holds every ink to its floor on every ground it is promised on. It is the
// one place a palette's pairs are measured: the constant ramp, a theme's own
// chrome surface and the light-ground ramp all come through here, so none of
// them can drift from the others.
//
// At 256 colours the grounds are expected on the grey ramp already (see To256)
// and every measurement is of the palette entry that will be drawn. At 16
// colours nothing is derived by blending: the grounds are the terminal's own,
// and the tokens are slots and attributes.
func Derive(p Palette) Palette {
	d := CurrentDepth()
	p.Depth = d
	if d == Depth16 {
		return derive16(p)
	}
	if p.RowSelQuiet == nil {
		p.RowSelQuiet = MixColors(p.Surface, p.RowSel, 0.5)
	}
	if p.Hover == nil {
		p.Hover = MixColors(p.Surface, ContrastText(p.Surface), hoverWash)
	}
	p.AccentTint = MixColors(p.Accent, p.Surface, statusTint)
	p.SuccessTint = MixColors(p.Success, p.Surface, statusTint)
	p.WarningTint = MixColors(p.Warning, p.Surface, statusTint)
	p.WarnTint = MixColors(p.Warn, p.Surface, statusTint)
	if d == Depth256 {
		// Each step is kept at least one grey away from the surface it sits
		// on, in the direction it had before it was placed: two neighbours in
		// truecolor can land on the same grey, and a cursor row the colour of
		// the surface is no cursor at all.
		s := To256(p.Surface)
		p.Canvas, p.Panel = apart256(p.Canvas, s), apart256(p.Panel, s)
		p.RowSel, p.Card = apart256(p.RowSel, s), apart256(p.Card, s)
		p.RowSelQuiet, p.Hover = apart256(p.RowSelQuiet, s), apart256(p.Hover, s)
		p.Surface = s
		p.AccentTint, p.SuccessTint = To256(p.AccentTint), To256(p.SuccessTint)
		p.WarningTint, p.WarnTint = To256(p.WarningTint), To256(p.WarnTint)
	}
	p.Edge = Shown(Structure(p.Surface))
	p.ScrollTrack = p.Edge
	p.checkContrast()
	p.ScrollThumb = p.FgMute
	p.PillFg = Shown(ContrastText(Shown(p.Accent)))
	return p
}

// hoverWash is how far Hover carries Surface toward its text end: the 6% wash
// the pointer leaves in every reference app surveyed for this.
const hoverWash = 0.06

// statusTint is how far a status tint is carried into Surface.
const statusTint = 0.7

// checkContrast lifts each ink that misses its floor on a ground it is
// promised on. The primary and secondary inks are text and hold ContrastFloor
// on every ground a row can take; the quiet ink is held to MarkFloor on the
// surface, since it marks and labels furniture rather than content.
func (p *Palette) checkContrast() {
	grounds := [...]color.Color{p.Surface, p.RowSel, p.RowSelQuiet, p.Hover}
	for _, g := range grounds {
		p.Fg = ReadableAt(p.Fg, g, ContrastFloor)
		p.FgDim = ReadableAt(p.FgDim, g, ContrastFloor)
	}
	p.Fg = ReadableAt(p.Fg, p.Card, ContrastFloor)
	p.FgMute = ReadableAt(p.FgMute, p.Surface, MarkFloor)
}

// derive16 is Derive at 16 colours. Every ground is the terminal's own
// background, so a panel frames itself and a row says what it is with reverse
// video or an underline rather than a fill (see RowState).
func derive16(p Palette) Palette {
	p.Canvas, p.Panel, p.Surface, p.RowSel, p.Card = NoColor, NoColor, NoColor, NoColor, NoColor
	p.RowSelQuiet, p.Hover = NoColor, NoColor
	p.AccentTint, p.SuccessTint, p.WarningTint, p.WarnTint = NoColor, NoColor, NoColor, NoColor
	p.Edge, p.ScrollTrack, p.ScrollThumb = Slot(8), Slot(8), Slot(8)
	// A chip is drawn in reverse video at this depth (see Chip), so its text
	// is the terminal's own background and needs no colour of its own.
	p.PillFg = NoColor
	p.Framed = true
	return p
}

// apart256 is To256(c), moved one step along the grey ramp when it lands on the
// same entry as surface. The step goes the way c lies from surface in
// truecolor, or away from the surface's own end when the two were equal.
func apart256(c, surface color.Color) color.Color {
	q := To256(c)
	if q != surface {
		return q
	}
	g, ok := q.(ansi.IndexedColor)
	if !ok || g < 232 {
		return q
	}
	dir := 1
	switch lc, ls := relativeLuminance(c), relativeLuminance(surface); {
	case lc < ls:
		dir = -1
	case lc == ls && ls > 0.18:
		dir = -1
	}
	return Grey(int(g) - 232 + dir)
}
