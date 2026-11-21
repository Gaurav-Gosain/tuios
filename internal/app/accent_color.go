package app

import (
	"image/color"
	"math"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// Accent is the colour a window wears on the rail. It is one of two things,
// and both have to keep working: an index into the theme's ANSI slots, which
// is what every accent set before the picker could reach the whole colour
// space is, or a literal RGB value, which is what the picker produces now.
//
// A slot re-resolves against whatever theme is loaded, so a row keeps its
// identity across a theme switch. An RGB accent is the colour the user chose
// and stays that colour. Keeping the two apart rather than converting the old
// ones to hex on load is what lets an existing accents file round-trip: a slot
// stays a slot until the user picks something else.
//
// Slot is the discriminator. A non-negative Slot selects an ANSI slot and R, G,
// B are unused; Slot < 0 means R, G, B carry the colour. The zero value is slot
// 0, bright black, exactly as a stored index of 0 has always meant.
type Accent struct {
	Slot    int
	R, G, B uint8
}

// SlotAccent is the accent for an ANSI slot index, the shape every accent had
// before the picker.
func SlotAccent(idx int) Accent { return Accent{Slot: clampInt(idx, 0, accentSwatchCount-1)} }

// RGBAccent is the accent for a literal colour.
func RGBAccent(c color.RGBA) Accent { return Accent{Slot: -1, R: c.R, G: c.G, B: c.B} }

// IsSlot reports whether the accent names a theme slot rather than a colour.
func (a Accent) IsSlot() bool { return a.Slot >= 0 }

// RGB resolves the accent to concrete channels, taking a slot's colour from the
// live theme.
func (a Accent) RGB() color.RGBA {
	if a.IsSlot() {
		return toRGBA(accentColor(a.Slot))
	}
	return color.RGBA{R: a.R, G: a.G, B: a.B, A: 0xff}
}

// Color is the accent as something lipgloss can paint with, already stepped
// down to what the terminal can show so the swatch and the hex beside it are
// the same colour.
//
// A slot with no theme loaded is the user terminal's own colour and travels as
// an index, so the host paints it from the user's palette. Resolving it to RGB
// here would show a colour off the xterm default table instead of the red the
// user actually has.
func (a Accent) Color() color.Color {
	if c, ok := a.slotIndex(); ok {
		return c
	}
	return accentShown(a.RGB())
}

func (a Accent) slotIndex() (color.Color, bool) {
	if !a.IsSlot() || theme.IsEnabled() {
		return nil, false
	}
	c, ok := accentColor(a.Slot).(ansi.BasicColor)
	return c, ok
}

// Hex is the accent's colour as #rrggbb.
func (a Accent) Hex() string { return overlay.Hex(a.RGB()) }

// Label is what a readout prints beside the accent's swatch. A slot the user's
// terminal owns is named rather than given a hex, because the hex would be the
// xterm default's and not the colour on screen.
func (a Accent) Label() string {
	if _, ok := a.slotIndex(); ok {
		return accentSlotNames[clampInt(a.Slot, 0, accentSwatchCount-1)]
	}
	return a.Hex()
}

func (a Accent) fold() uint64 {
	if a.IsSlot() {
		return uint64(a.Slot)
	}
	return 1<<32 | uint64(a.R)<<16 | uint64(a.G)<<8 | uint64(a.B)
}

func toRGBA(c color.Color) color.RGBA {
	r, g, b, _ := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xff}
}

func parseHexColor(s string) (color.RGBA, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	if strings.HasPrefix(s, "#") {
		return color.RGBA{}, false
	}
	return overlay.ParseHex(s)
}

func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func hslToRGB(h, s, l float64) color.RGBA {
	h = math.Mod(math.Mod(h, 360)+360, 360)
	s = math.Min(math.Max(s, 0), 1)
	l = math.Min(math.Max(l, 0), 1)
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	round := func(v float64) uint8 { return uint8(math.Round((v + m) * 255)) }
	return color.RGBA{R: round(r), G: round(g), B: round(b), A: 0xff}
}

func rgbToHSL(c color.RGBA) (h, s, l float64) {
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	maxV := math.Max(r, math.Max(g, b))
	minV := math.Min(r, math.Min(g, b))
	l = (maxV + minV) / 2
	d := maxV - minV
	if d == 0 {
		return 0, 0, l
	}
	s = d / (1 - math.Abs(2*l-1))
	switch maxV {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h, math.Min(s, 1), l
}

// SetAccentColorProfile pins the colour profile the accent picker paints and
// labels through, which is the one the chrome is drawn for (see
// theme.SetColorProfile). Exported for the servers, which learn it from their
// session, and for the tests that have to prove the fallback, which cannot be
// done against whatever terminal happens to run them.
func SetAccentColorProfile(p colorprofile.Profile) { theme.SetColorProfile(p) }

func accentColorProfile() colorprofile.Profile {
	p := theme.ColorProfile()
	if p <= colorprofile.ASCII {
		p = colorprofile.TrueColor
	}
	return p
}

func accentShown(c color.RGBA) color.Color {
	return accentColorProfile().Convert(c)
}

func accentFallbackLabel(c color.RGBA) string {
	switch v := accentShown(c).(type) {
	case ansi.IndexedColor:
		return "256:" + strconv.Itoa(int(v))
	case ansi.BasicColor:
		return "ansi:" + strconv.Itoa(int(v))
	default:
		return ""
	}
}

func accentContrast(c color.RGBA) color.Color { return theme.ContrastText(c) }

func accentMonochrome() bool { return accentColorProfile() <= colorprofile.ASCII }
