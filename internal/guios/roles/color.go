package roles

import (
	"image/color"
	"math"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Color is an sRGB colour with straight alpha, as MyGo's ui.Color without
// its wide gamut part. Mix, Over and Alpha do what MyGo's do.
type Color struct{ R, G, B, A uint8 }

// RGB returns an opaque colour.
func RGB(r, g, b uint8) Color { return Color{r, g, b, 255} }

// RGBA returns a colour with alpha between 0 and 1.
func RGBA(r, g, b uint8, alpha float32) Color { return Color{r, g, b, alphaByte(alpha)} }

// Hex parses "#rrggbb". It panics on other input, which is a mistake in
// the program.
func Hex(s string) Color {
	v, err := strconv.ParseUint(strings.TrimPrefix(s, "#"), 16, 32)
	if err != nil || len(s) != 7 {
		panic("roles: invalid colour " + s)
	}
	return RGB(uint8(v>>16), uint8(v>>8), uint8(v))
}

func alphaByte(a float32) uint8 {
	if a <= 0 {
		return 0
	}
	if a >= 1 {
		return 255
	}
	return uint8(a*255 + 0.5)
}

// Alpha returns the colour with its alpha multiplied by a.
func (c Color) Alpha(a float32) Color {
	c.A = alphaByte(float32(c.A) / 255 * a)
	return c
}

// Mix returns the colour t of the way from c to o.
func (c Color) Mix(o Color, t float32) Color {
	t = max(0, min(t, 1))
	m := func(a, b uint8) uint8 { return uint8(float32(a)*(1-t) + float32(b)*t + 0.5) }
	return Color{m(c.R, o.R), m(c.G, o.G), m(c.B, o.B), m(c.A, o.A)}
}

// Over returns c composited over the opaque colour base.
func (c Color) Over(base Color) Color {
	o := c.Mix(base, 1-float32(c.A)/255)
	o.A = base.A
	return o
}

// Oklch returns the colour of CSS's oklch(l c h), as MyGo's ui.Oklch
// shows it on a window that draws sRGB: mapped into Display P3, and a
// colour P3 holds but sRGB does not mapped to the nearest sRGB colour.
func Oklch(l, c, h float32) Color {
	r, g, b := gamutMap(displayP3, float64(l), float64(c), float64(h))
	r, g, b = linearP3ToSRGB(r, g, b)
	er, eg, eb := encode(r), encode(g), encode(b)
	// MyGo keeps the extended components in fixed point, 1/8192 a step.
	q := func(v float64) float64 { return math.Round(v*8192) / 8192 }
	er, eg, eb = q(er), q(eg), q(eb)
	const eps = 1.0 / 512
	in := func(v float64) bool { return v >= -eps && v <= 1+eps }
	if in(er) && in(eg) && in(eb) {
		byteOf := func(v float64) uint8 { return uint8(float32(max(0, min(v, 1)))*255 + 0.5) }
		return RGB(byteOf(er), byteOf(eg), byteOf(eb))
	}
	ll, cc, hh := oklchOf(oklab(decode(er), decode(eg), decode(eb)))
	lr, lg, lb := gamutMap(srgb, ll, cc, hh)
	byteOf := func(v float64) uint8 { return uint8(encode(v)*255 + 0.5) }
	return RGB(byteOf(lr), byteOf(lg), byteOf(lb))
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// Term is a terminal theme: what a pane is painted with, and what the
// roles derive from. Its fields are named as MyGo's terminal.Theme's.
type Term struct {
	Foreground, Background, Cursor, CursorText, Selection, SelectionText Color
	Palette                                                              [16]Color
}

// FromTheme is a tuios theme as a terminal theme, false for none or one
// with no foreground or background.
func FromTheme(id string) (*Term, bool) {
	if id == "" {
		return nil, false
	}
	theme.EnsureRegistry()
	pal, fg, bg, cursor, ok := theme.Colors(id)
	if !ok || fg == nil || bg == nil {
		return nil, false
	}
	th := &Term{Foreground: fromColor(fg), Background: fromColor(bg)}
	for i, c := range pal {
		if c != nil {
			th.Palette[i] = fromColor(c)
		}
	}
	th.Cursor = th.Foreground
	if cursor != nil {
		th.Cursor = fromColor(cursor)
	}
	th.CursorText = th.Background
	th.Selection = th.Palette[4].Mix(th.Background, 0.6)
	th.SelectionText = th.Foreground
	return th, true
}

func fromColor(c color.Color) Color {
	r, g, b, _ := c.RGBA()
	return RGB(uint8(r>>8), uint8(g>>8), uint8(b>>8))
}

// Look is the roles of a terminal theme, with its accent and the window's
// ground a shade under its cards. The accent is the cursor's colour, unless
// that is a grey, as many themes' cursors are, when it is the theme's blue.
func Look(th *Term) (p *Palette, accent, ground Color) {
	bl, bc, bh := ToOklch(th.Background)
	accent = th.Cursor
	if accent == (Color{}) {
		accent = th.Foreground
	}
	if _, c, _ := ToOklch(accent); c < 0.05 {
		accent = th.Palette[4]
	}
	p = Tinted(th, accent)
	shade := float32(0.03)
	if p.Dark {
		shade = 0.04
	}
	return p, accent, Oklch(max(bl-shade, 0), bc, bh)
}

// Tilly is guios's own theme, light or dark, after tuios's mascot: lilac
// on a deep violet ground in the dark, and on white cards over a pale
// lilac ground in the light (direction A of the 2026-11-07 research). The
// roles are set by hand, not derived: the text tiers, the accent, the
// raised surfaces and the four state colours. It shows where tuios names
// no theme.
func Tilly(dark bool) (th *Term, p *Palette, ground Color) {
	t := tillyLight
	if dark {
		t = tillyDark
	}
	th = &Term{Foreground: t.text, Background: t.card, Cursor: t.accent, CursorText: t.card, SelectionText: t.text}
	for i, h := range t.ansi {
		th.Palette[i] = Hex(h)
	}
	th.Selection = t.accent.Mix(t.card, 0.7)
	p = Tinted(th, t.accent)
	p.Sidebar = t.ground
	p.Popover = t.raised
	p.Work = t.work
	p.Attention = t.need
	p.AttnChip = t.need
	p.Done = t.done
	p.Danger = t.err
	if dark {
		p.AttnSoft = t.need.Alpha(0.15)
	} else {
		p.AttnSoft = t.need.Alpha(0.12)
	}
	p.tiers = [2]Color{t.text2, t.text3}
	p.finish(t.text, t.card)
	return th, p, t.ground
}

// tillyLook is the colours of one appearance of Tilly.
type tillyLook struct {
	ground, card, raised, text, text2, text3 Color
	accent, work, need, done, err            Color
	// ansi is the terminal's 16 colours, Catppuccin's (Mocha in the
	// dark, Latte in the light) as tuios's banner uses them.
	ansi [16]string
}

var tillyDark = tillyLook{
	ground: Hex("#14131c"), card: Hex("#1b1a25"), raised: Hex("#24222f"),
	text: Hex("#ece9f5"), text2: Hex("#a8a3ba"), text3: Hex("#8a85a0"),
	accent: Hex("#b99cf5"), work: Hex("#89dceb"), need: Hex("#f9cf7a"), done: Hex("#a6e3a1"), err: Hex("#f38ba8"),
	ansi: [16]string{"#45475a", "#f38ba8", "#a6e3a1", "#f9e2af", "#89b4fa", "#cba6f7", "#94e2d5", "#bac2de",
		"#585b70", "#f38ba8", "#a6e3a1", "#f9e2af", "#89b4fa", "#cba6f7", "#94e2d5", "#a6adc8"},
}

var tillyLight = tillyLook{
	ground: Hex("#f6f4fb"), card: Hex("#ffffff"), raised: Hex("#ffffff"),
	text: Hex("#1d1a29"), text2: Hex("#5d5870"), text3: Hex("#6f6a82"),
	accent: Hex("#7a4fd6"), work: Hex("#17768c"), need: Hex("#93600f"), done: Hex("#25733a"), err: Hex("#c4314b"),
	ansi: [16]string{"#5c5f77", "#d20f39", "#40a02b", "#c27b0f", "#1e66f5", "#8839ef", "#179299", "#acb0be",
		"#6c6f85", "#d20f39", "#40a02b", "#c27b0f", "#1e66f5", "#8839ef", "#179299", "#bcc0cc"},
}
