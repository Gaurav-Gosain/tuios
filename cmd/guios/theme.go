package main

import (
	"image/color"
	"math"

	"github.com/charmbracelet/x/ansi"
	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// roles are the colours of direction A (chrome-mocks/TOKENS.md, section 1),
// derived from the tuios theme in force: the terminal's colours, the 16 ANSI
// colours and the chrome palette tuios derives for its own rail. A theme,
// a gallery look or an Omarchy palette changes every role at once.
type roles struct {
	light bool
	fg    ui.Color // the terminal's default foreground
	bg    ui.Color // the terminal's default background
	cur   ui.Color
	ansi  [16]ui.Color

	shell  ui.Color // the sidebar and the band
	stage  ui.Color // a card and its header
	raised ui.Color // menus, toasts, sheets

	text, text2, text3 ui.Color

	hover, selected, control, active ui.Color
	hairline, lineCard, lineFocus    ui.Color

	accent, need, done, err, work ui.Color
	needEdge, needHalo            ui.Color
	veil                          ui.Color
}

func toUI(c color.Color, def ui.Color) ui.Color {
	if c == nil {
		return def
	}
	r, g, b, a := c.RGBA()
	if a == 0 {
		return def
	}
	return ui.Color{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 255}
}

// mix blends a toward b by t in sRGB.
func mix(a, b ui.Color, t float32) ui.Color {
	f := func(x, y uint8) uint8 { return uint8(math.Round(float64(float32(x)*(1-t) + float32(y)*t))) }
	return ui.Color{R: f(a.R, b.R), G: f(a.G, b.G), B: f(a.B, b.B), A: 255}
}

func alpha(c ui.Color, a float32) ui.Color {
	c.A = uint8(math.Round(float64(a * 255)))
	return c
}

func luminance(c ui.Color) float64 {
	lin := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

func hexColor(v uint32) ui.Color { return ui.RGB(uint8(v>>16), uint8(v>>8), uint8(v)) }

// dusk is guios's own palette when the config names no tuios theme: a cool
// slate with a violet accent, the default of the GPUI guios too.
var dusk = struct {
	fg, bg, cursor, accent, need, done, err, shell, raised uint32
	ansi                                                   [16]uint32
}{
	fg: 0xd8dbee, bg: 0x232637, cursor: 0xb3a8ff, accent: 0x9b8cff,
	need: 0xf2c063, done: 0x7fd49b, err: 0xf27a8a, shell: 0x1c1f2d, raised: 0x2d3146,
	ansi: [16]uint32{0x30344a, 0xf07d8c, 0x8bd59a, 0xeec677, 0x7ea8ff, 0xbf9bff, 0x6fcfd6, 0xc3c7dd,
		0x737a9a, 0xff98a6, 0xa9e5b3, 0xf6d79b, 0x9fbeff, 0xd4b9ff, 0x92e0e5, 0xeef0fa},
}

// currentRoles derives the roles from the theme in force.
func currentRoles() roles {
	if !theme.IsEnabled() {
		return duskRoles()
	}
	var r roles
	black, white := ui.RGB(0, 0, 0), ui.RGB(255, 255, 255)
	r.fg = toUI(theme.TerminalFg(), ui.RGB(0xd9, 0xdb, 0xe7))
	r.bg = toUI(theme.TerminalBg(), ui.RGB(0x23, 0x26, 0x37))
	r.cur = toUI(theme.TerminalCursor(), r.fg)
	pal := theme.GetANSIPalette()
	for i := range r.ansi {
		var c color.Color
		if theme.IsEnabled() {
			c = pal[i]
		}
		if c == nil {
			c = ansi.IndexedColor(i)
		}
		r.ansi[i] = toUI(c, r.fg)
	}
	r.light = luminance(r.bg) > 0.4
	gp := theme.GroundUI()
	shown := func(c color.Color, def ui.Color) ui.Color { return toUI(overlay.Shown(c), def) }

	r.stage = r.bg
	ink := r.fg
	if r.light {
		r.shell = mix(r.bg, black, 0.035)
		r.raised = mix(r.bg, white, 0.85)
		r.hover, r.selected, r.control, r.active = alpha(black, .04), alpha(black, .07), alpha(black, .05), alpha(black, .07)
		r.hairline, r.lineCard, r.lineFocus = alpha(black, .08), alpha(black, .06), alpha(black, .14)
	} else {
		r.shell = mix(r.bg, black, 0.20)
		r.raised = mix(r.bg, r.fg, 0.045)
		r.hover, r.selected, r.control, r.active = alpha(ink, .05), alpha(ink, .09), alpha(ink, .06), alpha(ink, .08)
		r.hairline, r.lineCard, r.lineFocus = alpha(ink, .07), alpha(ink, .06), alpha(ink, .16)
	}
	r.text = r.fg
	r.text2 = mix(r.fg, r.bg, 0.30)
	r.text3 = mix(r.fg, r.bg, 0.48)

	r.accent = shown(gp.Accent, r.ansi[4])
	r.need = shown(gp.Attention, r.ansi[3])
	r.done = shown(gp.Success, r.ansi[2])
	r.err = shown(gp.Warn, r.ansi[1])
	r.work = r.ansi[4]
	r.needEdge = alpha(r.need, .70)
	r.needHalo = alpha(r.need, .10)
	r.veil = alpha(r.stage, .30)
	return r
}

// cellColor resolves a cell's colour: nil is the default, the 16 ANSI
// colours come from the theme, the rest are as the program gave them.
func (r *roles) cellColor(c color.Color, def ui.Color) ui.Color {
	switch v := c.(type) {
	case nil:
		return def
	case ansi.BasicColor:
		return r.ansi[int(v)&15]
	case ansi.IndexedColor:
		if v < 16 {
			return r.ansi[v]
		}
	}
	return toUI(c, def)
}

// stateColor is the colour of an agent state's mark.
func (r *roles) stateColor(state string) ui.Color {
	switch state {
	case "needs_input":
		return r.need
	case "working":
		return r.work
	case "done":
		return r.done
	case "errored":
		return r.err
	}
	return r.text3
}

func duskRoles() roles {
	var r roles
	d := dusk
	r.fg, r.bg, r.cur = hexColor(d.fg), hexColor(d.bg), hexColor(d.cursor)
	for i, v := range d.ansi {
		r.ansi[i] = hexColor(v)
	}
	ink := r.fg
	r.stage, r.shell, r.raised = r.bg, hexColor(d.shell), hexColor(d.raised)
	r.hover, r.selected, r.control, r.active = alpha(ink, .05), alpha(ink, .09), alpha(ink, .06), alpha(ink, .08)
	r.hairline, r.lineCard, r.lineFocus = alpha(ink, .07), alpha(ink, .06), alpha(ink, .16)
	r.text, r.text2, r.text3 = r.fg, mix(r.fg, r.bg, 0.30), mix(r.fg, r.bg, 0.48)
	r.accent, r.need, r.done, r.err, r.work = hexColor(d.accent), hexColor(d.need), hexColor(d.done), hexColor(d.err), r.ansi[4]
	r.needEdge, r.needHalo, r.veil = alpha(r.need, .70), alpha(r.need, .10), alpha(r.stage, .30)
	return r
}
