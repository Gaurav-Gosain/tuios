// Package overlay provides composable, framework-agnostic building blocks for
// borderless floating overlay panels rendered with charm.land/lipgloss/v2.
//
// The design is deliberately borderless: a panel is a solid Surface-filled
// rectangle whose neutrals step by luminance (Canvas < Panel < Surface < Card)
// so it reads as a raised, floating surface without box-drawing characters.
// Selection is shown with a full-width highlight bar rather than an arrow, and
// an inset accent title chip identifies the panel.
//
// Every renderer returns both the rendered string and a Geometry describing the
// panel-relative rectangles of its interactive regions (title bar, tabs, body
// origin), so a host can hit-test mouse events without duplicating layout math.
//
// The package holds no global state and depends only on lipgloss and the
// standard library, so it can be lifted out into a standalone module.
package overlay

import "image/color"

// Palette is the semantic color set a panel is rendered with. Callers provide
// it, keeping this package independent of any particular theming system.
//
// The neutral ramp (Canvas, Panel, Surface, RowSel, Card) should step by
// luminance so surfaces read as layered without borders. Accent carries "this
// is the interactive thing"; Warn is reserved for destructive actions.
type Palette struct {
	Canvas   color.Color // darkest base
	Panel    color.Color // outer band / muted panel base
	Surface  color.Color // the floating panel fill
	RowSel   color.Color // selected-row highlight bar (pressed-in)
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
}
