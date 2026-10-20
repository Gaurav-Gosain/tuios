package overlay

import (
	"image/color"
	"strings"
	"sync/atomic"

	"charm.land/lipgloss/v2"
)

// Chrome is the handful of characters the overlay family draws its own
// furniture with. A host that lets the user choose a glyph set pushes one in;
// with none pushed every helper draws what it always drew.
//
// It is pushed rather than pulled for the same reason SetASCII is: this package
// depends on nothing inside tuios so that it can be lifted out, and the registry
// that resolves a named set lives above it. An empty field means "keep the
// built-in", so a set that renames two glyphs is two fields rather than a full
// table.
type Chrome struct {
	Ellipsis   string // truncation marker
	Sigil      string // the mark fronting an input or a cursor row
	ArrowLeft  string // cycler and tab-strip overflow, pointing back
	ArrowRight string // the same, pointing on
	Rule       string // a panel's solid divider
	DashRule   string // a dialog's lighter divider
}

// chrome holds the pushed set. A pointer so the zero state is "nothing pushed"
// and a swap is one store; atomic because every session's render loop reads it
// while a set-config on another session writes it.
var chrome atomic.Pointer[Chrome]

// SetChrome records the glyphs the overlay furniture is drawn with. A nil
// argument clears back to the built-ins.
func SetChrome(c *Chrome) { chrome.Store(c) }

// chromeOr returns the pushed glyph for one role, or def when no set is pushed
// or the set leaves that role alone.
//
// Callers check UseASCII before reaching here, so a pushed glyph outside 7-bit
// loses to ASCII mode. That is deliberate: the set a user chose says what their
// font can draw, and --ascii-only says the running terminal cannot draw it,
// which is the narrower claim and the one to believe. The test is per glyph
// rather than per set, so a set that is ASCII in the roles it can be keeps them
// in a terminal that has to be.
func chromeOr(role func(*Chrome) string, def string) string {
	if c := chrome.Load(); c != nil {
		if g := role(c); g != "" && (!UseASCII() || IsASCII(g)) {
			return g
		}
	}
	return def
}

// IsASCII reports whether every rune of s is 7-bit, which is the test for
// whether a glyph can be drawn in a terminal that has told us it cannot manage
// more.
func IsASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

// Frame is the glyphs a dialog's border is drawn with, and a panel's when it
// is framed: four corners, the horizontal and the vertical. A host pushes the
// set its border style resolves to, for the same reason it pushes Chrome.
type Frame struct {
	TopLeft, TopRight, BottomLeft, BottomRight string
	Horizontal, Vertical                       string
}

// frame holds the pushed Frame; nil draws the rounded built-in.
var frame atomic.Pointer[Frame]

// SetFrame records the glyphs dialog and panel frames are drawn with. A host
// calls it every frame, so an unchanged set is not stored again, and is not
// copied to the heap either: only the stored copy escapes.
func SetFrame(f Frame) {
	if cur := frame.Load(); cur != nil && *cur == f {
		return
	}
	stored := f
	frame.Store(&stored)
}

// framePanels says a panel draws a frame round itself at every colour depth,
// not only at 16 colours where its surface needs one.
var framePanels atomic.Bool

// SetFramePanels records whether every panel is framed. Derive reads it into
// Palette.Framed.
func SetFramePanels(on bool) { framePanels.Store(on) }

// FramePanels reports what SetFramePanels last recorded, so a host that
// memoises a palette can key it on the answer.
func FramePanels() bool { return framePanels.Load() }

// FrameColors is what an overlay's frame is drawn in when a host pushes it: Ink
// for the glyphs, and Ground for the cells under them. A host pushes the ink
// its focused pane's border is drawn in, because an open overlay is the focused
// thing, and a ground its compositor reads as the cell beneath the overlay, so
// the frame sits on the panes the way a pane's own border does. A nil Ink keeps
// the palette's; a nil Ground leaves the frame cells on the terminal's default
// background.
type FrameColors struct {
	Ink, Ground color.Color
}

// frameColors holds the pushed FrameColors; nil is the zero value.
var frameColors atomic.Pointer[FrameColors]

// SetFrameColors records the colours overlay frames are drawn in. A host calls
// it every frame; an unchanged pair is not stored again.
func SetFrameColors(c FrameColors) {
	if cur := frameColors.Load(); cur != nil && *cur == c {
		return
	}
	stored := c
	frameColors.Store(&stored)
}

// frameStyle is the style an overlay's outer frame is drawn in: the pushed ink,
// or fallback when none is pushed, on the pushed ground rather than the
// surface's bg. A blank frame (the hidden border style) is part of the
// surface's fill, the fill being its edge, so it keeps bg.
func frameStyle(bg, fallback color.Color, glyphs ...string) lipgloss.Style {
	var c FrameColors
	if p := frameColors.Load(); p != nil {
		c = *p
	}
	ink := fallback
	if c.Ink != nil {
		ink = c.Ink
	}
	if strings.TrimSpace(strings.Join(glyphs, "")) == "" {
		return Style(bg).Foreground(ink)
	}
	st := lipgloss.NewStyle().Foreground(ink)
	if c.Ground != nil {
		st = st.Background(c.Ground)
	}
	return st
}
