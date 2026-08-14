package vis

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The display borrows the app's own chrome: theme.UI() for the palette and
// internal/overlay for the primitives, so the harness reads as the same product
// as the thing it is hammering. Nothing here is a new visual language.

// palette returns the chrome palette, optionally flattened to a monochrome
// ramp. Monochrome is not a cosmetic mode: it is the check that no message on
// this screen is carried by hue alone. Every colour collapses onto the ramp it
// already sat on by luminance, so a frame that still reads in monochrome is a
// frame whose alarm is words, glyphs and weight.
func palette(mono bool) overlay.Palette {
	p := theme.UI()
	if !mono {
		return p
	}
	grey := func(v uint8) color.Color { return color.RGBA{R: v, G: v, B: v, A: 0xFF} }
	p.Canvas, p.Panel, p.Surface = grey(0x0d), grey(0x1c), grey(0x2a)
	p.RowSel, p.Card = grey(0x1c), grey(0x33)
	p.Fg, p.FgDim, p.FgMute = grey(0xf2), grey(0xb0), grey(0x6a)
	// The four semantic inks land on three brightness steps. Cherry and Julep
	// have to differ from the body text or a verdict would read as a label, and
	// they cannot differ from each other by hue here, so the words carry it.
	p.Accent, p.Selected = grey(0x55), grey(0x55)
	p.AccentBright = grey(0xd8)
	p.PillFg = grey(0xf2)
	p.Warn, p.Success, p.Warning, p.Info = grey(0xff), grey(0xd8), grey(0xd8), grey(0xd8)
	return p
}

// glyphset is every non-ASCII mark the display draws. It is one struct rather
// than scattered literals because the ASCII pass asserts the whole frame holds
// no rune above 127, and a glyph chosen at its use site is a glyph that escapes
// that assertion.
type glyphset struct {
	tl, tr, bl, br, h, v string // the harness frame
	dot, fail, hold      string // an invariant at rest, broken, and a held candidate
	live                 string // the shrink candidate being replayed now
	block                string // a full bar cell
	eighths              []string
	arrow, times, sep    string
	pass                 string
	head                 string // the tape's write head when reverse video is unavailable
}

var unicodeGlyphs = glyphset{
	tl: "╭", tr: "╮", bl: "╰", br: "╯", h: "─", v: "│",
	dot: "·", fail: "✕", hold: "○", live: "▸",
	block:   "█",
	eighths: []string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"},
	arrow:   "→", times: "×", sep: "·",
	pass: "✓", head: "▮",
}

var asciiGlyphs = glyphset{
	tl: "+", tr: "+", bl: "+", br: "+", h: "-", v: "|",
	dot: ".", fail: "x", hold: "o", live: ">",
	block: "#",
	// No sub-cell resolution in ASCII, so a bar is whole cells and the figure
	// beside it carries the precision the glyphs cannot.
	eighths: []string{"", "", "", "", "", "", "", ""},
	arrow:   "->", times: "x", sep: ".",
	pass: "ok", head: "#",
}

func glyphs() glyphset {
	if overlay.UseASCII() {
		return asciiGlyphs
	}
	return unicodeGlyphs
}

// row builds one line of a band: the fragments joined, then padded out to width
// with the band's own background. Every cell of a rail row carries Panel that
// way, which is the figure/ground split that stops the instruments being read
// as part of the app.
func row(width int, bg color.Color, parts ...string) string {
	return overlay.Fill(strings.Join(parts, ""), width, bg)
}

// ink is a fragment on a known ground. Foregrounds are always paired with their
// background here because a bare foreground emits a reset that punches a hole
// through the fill when the frame is composited.
func ink(bg, fg color.Color) lipgloss.Style {
	return overlay.Style(bg).Foreground(fg)
}

// bold is ink with weight, which is one of the three carriers the alarm uses so
// that it survives a monochrome terminal.
func bold(bg, fg color.Color) lipgloss.Style {
	return ink(bg, fg).Bold(true)
}

// launder strips what the fuzzer generates before it reaches the instruments.
// Action payloads are the fuzzer's own hostile strings: a Guest action's text is
// literally an escape sequence aimed at a terminal, and echoing one into the
// rail would let the system under test drive the instrument reading it. The rule
// is the one the sidebar applies to pane titles, which are foreign data for the
// same reason.
func launder(s string, maxWidth int) string {
	ascii := overlay.UseASCII()
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r < 0x20 || (r >= 0x7f && r < 0xa0):
			// C0 and C1 controls, the escape that starts a sequence among them.
			b.WriteByte('.')
		case ascii && r > 0x7e:
			b.WriteByte('.')
		case r >= 0xe000 && r <= 0xf8ff, r >= 0xf0000:
			// Private use, which tofus without the font that defines it.
			b.WriteByte('.')
		default:
			b.WriteRune(r)
		}
	}
	return overlay.Truncate(b.String(), maxWidth)
}

// commas groups a count so the eye can size it at a glance. Every figure on
// this screen is a running counter off the engine, and the difference between
// 12,540 and 125,460 is the whole point of showing it.
func commas(n int) string {
	if n < 0 {
		return "-" + commas(-n)
	}
	s := itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
