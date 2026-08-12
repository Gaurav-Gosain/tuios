package app

import (
	"image/color"
	"math"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

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
func (a Accent) Color() color.Color { return accentShown(a.RGB()) }

// Hex is the accent's colour as #rrggbb.
func (a Accent) Hex() string { return hexString(a.RGB()) }

// fold packs the accent into one value for the rail's render signature. A slot
// and an RGB value can never collide because a slot lands in the low byte with
// the tag bit clear.
func (a Accent) fold() uint64 {
	if a.IsSlot() {
		return uint64(a.Slot)
	}
	return 1<<32 | uint64(a.R)<<16 | uint64(a.G)<<8 | uint64(a.B)
}

// toRGBA flattens any color.Color to 8-bit channels.
func toRGBA(c color.Color) color.RGBA {
	r, g, b, _ := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xff}
}

// hexString formats a colour as #rrggbb, lowercase like the rest of the
// dialog's furniture.
func hexString(c color.RGBA) string {
	const digits = "0123456789abcdef"
	out := []byte("#000000")
	for i, v := range [3]uint8{c.R, c.G, c.B} {
		out[1+i*2] = digits[v>>4]
		out[2+i*2] = digits[v&0x0f]
	}
	return string(out)
}

// parseHexColor reads #rgb or #rrggbb, with or without the hash. The short form
// is expanded the way CSS expands it, so #f0a and #ff00aa are the same colour.
func parseHexColor(s string) (color.RGBA, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	var v [6]byte
	switch len(s) {
	case 3:
		for i := range 3 {
			v[i*2], v[i*2+1] = s[i], s[i]
		}
	case 6:
		copy(v[:], s)
	default:
		return color.RGBA{}, false
	}
	out := color.RGBA{A: 0xff}
	ch := [3]*uint8{&out.R, &out.G, &out.B}
	for i := range 3 {
		n, err := strconv.ParseUint(string(v[i*2:i*2+2]), 16, 8)
		if err != nil {
			return color.RGBA{}, false
		}
		*ch[i] = uint8(n)
	}
	return out, true
}

// isHexDigit reports whether r can go in the hex field.
func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// hslToRGB converts hue in degrees and saturation/lightness in 0..1.
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

// rgbToHSL is the inverse of hslToRGB. A grey has no hue to report, so it
// returns 0 there and callers keep whatever hue they were holding.
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

// rotateHue returns c with its hue turned by deg, keeping saturation and
// lightness. This is how the harmony chips are built.
func rotateHue(c color.RGBA, deg float64) color.RGBA {
	h, s, l := rgbToHSL(c)
	return hslToRGB(h+deg, s, l)
}

// accentProfile caches the probed colour profile. Zero is colorprofile.Unknown,
// which is what "not probed yet" looks like. Atomic because every session's
// render goroutine reads it.
var accentProfile atomic.Uint32

// SetAccentColorProfile pins the colour profile the accent picker paints and
// labels through. Exported for the tests that have to prove the fallback, which
// cannot be done against whatever terminal happens to run them.
func SetAccentColorProfile(p colorprofile.Profile) { accentProfile.Store(uint32(p)) }

// accentColorProfile is the colour profile the frame is written through.
//
// It matters here and nowhere else: the picker offers sixteen million colours,
// and on a terminal that cannot show them the writer quietly steps every one of
// them down on the way out. A picker that kept printing the exact hex next to a
// cell painted in some other colour would be lying about the one thing it is
// for. A pipe or a monochrome terminal has nothing to step down to, so those
// are treated as exact and the writer drops what it cannot show.
func accentColorProfile() colorprofile.Profile {
	if v := accentProfile.Load(); v != 0 {
		return colorprofile.Profile(v)
	}
	p := colorprofile.Detect(os.Stdout, os.Environ())
	if p <= colorprofile.ASCII {
		p = colorprofile.TrueColor
	}
	accentProfile.Store(uint32(p))
	return p
}

// accentShown is the colour a cell painted with c actually shows on this
// terminal: c itself with truecolour, the nearest palette entry without.
func accentShown(c color.RGBA) color.Color {
	return accentColorProfile().Convert(c)
}

// accentFallbackLabel names what c was stepped down to, or "" when the terminal
// can show c exactly. The picker prints it beside the hex so the user is told
// about the substitution rather than left to notice it.
func accentFallbackLabel(c color.RGBA) string {
	switch v := accentShown(c).(type) {
	case ansi.ExtendedColor:
		return "256:" + strconv.Itoa(int(v))
	case ansi.BasicColor:
		return "ansi:" + strconv.Itoa(int(v))
	default:
		return ""
	}
}

// accentContrast picks a marker colour that reads on a swatch, so the cursor is
// findable on a white cell and on a black one.
func accentContrast(c color.RGBA) color.Color { return theme.ContrastText(c) }
