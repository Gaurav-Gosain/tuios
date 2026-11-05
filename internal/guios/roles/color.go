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

// Paper is guios's own theme, light or dark: white paper on a desk of
// light grey, in ink, flat and quiet. It shows where tuios names no theme.
func Paper(dark bool) (th *Term, p *Palette, ground Color) {
	var accent Color
	th, accent = toneTheme(tone{260, 0, 0.9, 262}, dark)
	ground = Hex("#f6f6f7")
	if dark {
		ground = Hex("#141416")
	}
	return th, Tinted(th, accent), ground
}
