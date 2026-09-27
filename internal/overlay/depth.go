package overlay

import (
	"image/color"
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

// depth holds the active depth. Atomic because every session's render loop
// reads it and a client learning its terminal's profile writes it.
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

// isNoColor reports whether c is the terminal's default colour: nil, or a
// lipgloss.NoColor, which is what every ground is at 16 colours.
func isNoColor(c color.Color) bool {
	switch c.(type) {
	case nil, lipgloss.NoColor, *lipgloss.NoColor:
		return true
	}
	return false
}

// terminalOwned reports whether the terminal decides what c looks like: the
// default colour or one of the sixteen slots. tuios cannot measure such a
// colour, because the RGB it would measure is the xterm default and not what
// the user's palette holds.
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
// luminance, and anything else to the nearest entry colorprofile picks.
//
// colorprofile picks between the colour cube and the grey ramp by perceptual
// distance, and a grey with a slight blue cast, which is what the charmtone
// neutrals are, wins a blue cube entry: charmtone BBQ became index 17, navy.
// Deciding greys by chroma first is what keeps a neutral neutral.
func To256(c color.Color) color.Color {
	if isNoColor(c) {
		return c
	}
	switch v := c.(type) {
	case ansi.IndexedColor, ansi.BasicColor:
		return v
	}
	if okChroma(c) < greyChroma {
		return nearestGrey(c)
	}
	return colorprofile.ANSI256.Convert(c)
}

// greyChroma is the OKLab chroma below which a colour is placed on the grey
// ramp. charmtone's neutrals sit at 0.01 to 0.02 and the least saturated
// accent tuios ships at 0.06, so the line falls between them.
const greyChroma = 0.035

// nearestGrey returns the entry of the grey ramp, or black 16 or white 231 at
// the ends, whose luminance is nearest c's.
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
