package vis

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func palette(mono bool) overlay.Palette {
	p := theme.UI()
	if !mono {
		return p
	}
	grey := func(v uint8) color.Color { return color.RGBA{R: v, G: v, B: v, A: 0xFF} }
	p.Canvas, p.Panel, p.Surface = grey(0x0d), grey(0x1c), grey(0x2a)
	p.RowSel, p.Card = grey(0x1c), grey(0x33)
	p.Fg, p.FgDim, p.FgMute = grey(0xf2), grey(0xb0), grey(0x6a)
	p.Accent, p.Selected = grey(0x55), grey(0x55)
	p.AccentBright = grey(0xd8)
	p.PillFg = grey(0xf2)
	p.Warn, p.Success, p.Warning, p.Info = grey(0xff), grey(0xd8), grey(0xd8), grey(0xd8)
	return p
}

type glyphset struct {
	tl, tr, bl, br, h, v string
	dot, fail, hold      string
	live                 string
	block                string
	eighths              []string
	arrow, times, sep    string
	pass                 string
	head                 string
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
	block:   "#",
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

func row(width int, bg color.Color, parts ...string) string {
	return overlay.Fill(strings.Join(parts, ""), width, bg)
}

func ink(bg, fg color.Color) lipgloss.Style {
	return overlay.Style(bg).Foreground(fg)
}

func bold(bg, fg color.Color) lipgloss.Style {
	return ink(bg, fg).Bold(true)
}

func launder(s string, maxWidth int) string {
	ascii := overlay.UseASCII()
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r < 0x20 || (r >= 0x7f && r < 0xa0):
			b.WriteByte('.')
		case ascii && r > 0x7e:
			b.WriteByte('.')
		case r >= 0xe000 && r <= 0xf8ff, r >= 0xf0000:
			b.WriteByte('.')
		default:
			b.WriteRune(r)
		}
	}
	return overlay.Truncate(b.String(), maxWidth)
}

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
