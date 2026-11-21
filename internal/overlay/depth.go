package overlay

import (
	"image/color"
	"math"
	"sync"
	"sync/atomic"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// Depth is how many colours the frame reaches the terminal with. The chrome is
// designed separately for each one rather than drawn in truecolor and stepped
// down a colour at a time, because a stepped-down ramp is what turned panels
// navy at 256 colours and made them vanish at 16.
type Depth uint8

const (
	// DepthTrueColor is 24-bit colour: the chrome is drawn as designed.
	DepthTrueColor Depth = iota
	// Depth256 is the xterm 256-colour palette. Neutrals are placed on the
	// grey ramp (232 to 255) by hand, so no grey lands on a blue cube entry.
	Depth256
	// Depth16 is the sixteen ANSI colours, or no colour at all. Grounds are the
	// terminal's own background, hierarchy is carried by the default
	// foreground, bright black, bold and reverse video, and colours are ANSI
	// slot indices the terminal paints from its own palette.
	Depth16
)

// String names the depth the way the docs and the E2E artifacts do.
func (d Depth) String() string {
	switch d {
	case Depth256:
		return "256"
	case Depth16:
		return "16"
	}
	return "truecolor"
}

// DepthOf maps a detected colour profile to the chrome depth drawn for it.
//
// NoTTY and Unknown map to truecolor: nothing was detected, and drawing the
// designed chrome is what tuios did before the depth was known. ASCII maps to
// 16, because a terminal that asked for no colour (NO_COLOR) still shows bold
// and reverse, and those are all the 16-colour chrome needs to keep its
// structure.
func DepthOf(p colorprofile.Profile) Depth {
	switch p {
	case colorprofile.ANSI256:
		return Depth256
	case colorprofile.ANSI, colorprofile.ASCII:
		return Depth16
	}
	return DepthTrueColor
}

var depth atomic.Uint32

// SetDepth records the depth the chrome is drawn for. The palette, the
// contrast helpers and the row and panel renderers all read it.
func SetDepth(d Depth) { depth.Store(uint32(d)) }

// CurrentDepth is the depth the chrome is drawn for.
func CurrentDepth() Depth { return Depth(depth.Load()) }

// NoColor is the terminal's own default colour: no SGR is written for it, so
// the terminal paints it from its own settings. It is what a 16-colour ground
// is, and what a 16-colour primary ink is.
var NoColor color.Color = lipgloss.NoColor{}

// Slot is ANSI colour slot i (0 to 15), which the terminal paints from its own
// palette. The 16-colour chrome is built from these so a user's palette decides
// what the chrome looks like, as it does for everything else on their screen.
func Slot(i int) color.Color {
	// #nosec G115 -- i is clamped to [0, 15].
	return ansi.BasicColor(uint8(min(max(i, 0), 15)))
}

// Grey is xterm 256-colour grey ramp entry i, 0 to 23 (indices 232 to 255).
func Grey(i int) color.Color {
	// #nosec G115 -- i is clamped to [0, 23].
	return ansi.IndexedColor(uint8(232 + min(max(i, 0), 23)))
}

func isNoColor(c color.Color) bool {
	switch c.(type) {
	case nil, lipgloss.NoColor, *lipgloss.NoColor:
		return true
	}
	return false
}

// IsNoColor reports whether c is the terminal's default colour, for code
// outside this package that writes its own sequences and has to leave such a
// colour unset.
func IsNoColor(c color.Color) bool { return isNoColor(c) }

func terminalOwned(c color.Color) bool {
	if isNoColor(c) {
		return true
	}
	_, ok := c.(ansi.BasicColor)
	return ok
}

// Shown is c as the terminal will show it at the current depth: unchanged in
// truecolor, the palette entry the frame writer will pick at 256. Contrast is
// measured on what is shown, so a floor holds for the colour on screen rather
// than for one the terminal never draws.
func Shown(c color.Color) color.Color {
	if isNoColor(c) {
		return c
	}
	if CurrentDepth() == Depth256 {
		if _, ok := c.(ansi.IndexedColor); ok {
			return c
		}
		if _, ok := c.(ansi.BasicColor); ok {
			return c
		}
		return To256(c)
	}
	return c
}

// To256 places c on the xterm 256-colour palette the way the chrome wants it:
// a colour with little chroma goes to the nearest entry on the grey ramp by
// luminance, and anything else to the entry nearest it in OKLab.
//
// colorprofile picks between the colour cube and the grey ramp by perceptual
// distance, and a grey with a slight blue cast, which is what the charmtone
// neutrals are, wins a blue cube entry: charmtone BBQ became index 17, navy.
// Deciding greys by chroma first is what keeps a neutral neutral.
//
// colorprofile is not used for the colours either: it places some saturated
// colours on the grey ramp. Catppuccin Latte's red, #d20f38, becomes 241, a
// mid grey, so a removed line's number at 256 colours was grey.
func To256(c color.Color) color.Color {
	if isNoColor(c) {
		return c
	}
	switch v := c.(type) {
	case ansi.IndexedColor, ansi.BasicColor:
		return v
	}
	ch := okChroma(c)
	if ch < greyChroma {
		return nearestGrey(c)
	}
	q := nearest256(c)
	if ch < tintChroma {
		if g := nearestGrey(c); Distance(g, c) < Distance(q, c) {
			return g
		}
	}
	return q
}

const tintChroma = 0.06

var xterm256Lab = sync.OnceValue(func() (t [240]lab) {
	for i := range t {
		t[i] = toLab(ansi.IndexedColor(uint8(16 + i))) // #nosec G115 -- i is within [0, 239].
	}
	return t
})

func nearest256(c color.Color) color.Color {
	v := toLab(c)
	vc := math.Hypot(v.a, v.b)
	best, bestD := uint8(16), math.Inf(1)
	for _, e := range xterm256Coloured() {
		dl, da, db, dc := v.l-e.l, v.a-e.a, v.b-e.b, vc-e.c
		dh2 := max(da*da+db*db-dc*dc, 0)
		if d := dl*dl + dc*dc + hueWeight*dh2; d < bestD {
			best, bestD = e.idx, d
		}
	}
	return ansi.IndexedColor(best)
}

type colouredEntry struct {
	l, a, b, c float64
	idx        uint8
}

var xterm256Coloured = sync.OnceValue(func() []colouredEntry {
	table := xterm256Lab()
	out := make([]colouredEntry, 0, len(table))
	for i, e := range table {
		if neutralEntry(i) {
			continue
		}
		out = append(out, colouredEntry{l: e.l, a: e.a, b: e.b, c: math.Hypot(e.a, e.b), idx: uint8(16 + i)}) // #nosec G115 -- i is within [0, 239].
	}
	return out
})

const hueWeight = 4

func neutralEntry(i int) bool {
	if i >= 216 {
		return true
	}
	return i/36 == (i/6)%6 && (i/6)%6 == i%6
}

const greyChroma = 0.035

func nearestGrey(c color.Color) color.Color {
	target := relativeLuminance(c)
	best, bestD := ansi.IndexedColor(16), 2.0
	try := func(idx ansi.IndexedColor) {
		d := relativeLuminance(idx) - target
		if d < 0 {
			d = -d
		}
		if d < bestD {
			best, bestD = idx, d
		}
	}
	try(16)
	for i := 232; i <= 255; i++ {
		try(ansi.IndexedColor(uint8(i))) // #nosec G115 -- i is within [232, 255].
	}
	try(231)
	return best
}

// ReadableEntry256 is the entry of the 256-colour cube (16 to 231) nearest c
// in hue that clears floor on every ground, as the depth shows them. Hue comes
// before lightness: the entry has to be darker or lighter than c to read, and
// what should survive the step is the colour. Stepping c toward the text end
// and then to the palette drained its chroma below greyChroma on a light
// ground, so a blue key came out a grey one. It returns To256(c) when that
// already reads, and nil when no cube entry does.
func ReadableEntry256(c color.Color, grounds []color.Color, floor float64) color.Color {
	reads := func(q color.Color) bool {
		for _, g := range grounds {
			if ContrastRatio(q, Shown(g)) < floor {
				return false
			}
		}
		return true
	}
	if q := To256(c); reads(q) {
		return q
	}
	v := toLab(c)
	table := xterm256Lab()
	if okChroma(c) < greyChroma {
		var best color.Color
		bestD := math.Inf(1)
		for i, e := range table {
			if !neutralEntry(i) {
				continue
			}
			if d := math.Abs(v.l - e.l); d < bestD {
				q := ansi.IndexedColor(uint8(16 + i)) // #nosec G115 -- i is within [0, 239].
				if reads(q) {
					best, bestD = q, d
				}
			}
		}
		return best
	}
	hue := math.Atan2(v.b, v.a)
	var best color.Color
	bestD := math.Inf(1)
	for i := range 216 {
		e := table[i]
		if math.Hypot(e.a, e.b) < greyChroma {
			continue
		}
		dh := math.Abs(math.Atan2(e.b, e.a) - hue)
		if dh > math.Pi {
			dh = 2*math.Pi - dh
		}
		dl := v.l - e.l
		d := dh*dh + dl*dl
		if d >= bestD {
			continue
		}
		q := ansi.IndexedColor(uint8(16 + i)) // #nosec G115 -- i is within [0, 215].
		if reads(q) {
			best, bestD = q, d
		}
	}
	return best
}

const warningApartHue = 35

const amberHue = 75

const amberBand = 40

// WarningApart256 keeps a 256-colour warning from reading as the error ink.
//
// A light theme's yellow is often a rust or a brown, and on a light ground only
// the dark cube entries read. The entry nearest it in hue can then be a red
// beside the error's own (seoulbones_light, zenbones), so a warning and a
// failure looked the same. When warning sits within warningApartHue of errInk,
// it returns the entry from orange to olive nearest amber in hue that reads on
// every ground, nearest warning's lightness among equals.
//
// It returns warning unchanged when the two are apart, when either is a
// neutral, or when no amber reads. The last is left alone on purpose: on a
// light theme's cursor row no amber clears the mark floor for gruvbox_light or
// tokyo_night_light, and a warning that reads beats one that differs.
func WarningApart256(warning, errInk color.Color, grounds []color.Color, floor float64) color.Color {
	if warning == nil || errInk == nil || okChroma(warning) < greyChroma || okChroma(errInk) < greyChroma {
		return warning
	}
	w, e := toLab(warning), toLab(errInk)
	gap := math.Abs(math.Atan2(w.b, w.a) - math.Atan2(e.b, e.a))
	if gap > math.Pi {
		gap = 2*math.Pi - gap
	}
	if gap*180/math.Pi >= warningApartHue {
		return warning
	}
	table := xterm256Lab()
	var best color.Color
	bestD := math.Inf(1)
	for i := range 216 {
		if neutralEntry(i) {
			continue
		}
		v := table[i]
		hue := math.Atan2(v.b, v.a) * 180 / math.Pi
		if hue < amberHue-amberBand || hue > amberHue+amberBand {
			continue
		}
		q := ansi.IndexedColor(uint8(16 + i)) // #nosec G115 -- i is within [0, 215].
		readsAll := true
		for _, g := range grounds {
			if ContrastRatio(q, Shown(g)) < floor {
				readsAll = false
				break
			}
		}
		if !readsAll {
			continue
		}
		dh, dl := (hue-amberHue)/180*math.Pi, v.l-w.l
		if d := dh*dh + dl*dl; d < bestD {
			best, bestD = q, d
		}
	}
	if best != nil {
		return best
	}
	return warning
}
