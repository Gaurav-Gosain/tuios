package app

import (
	"image/color"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
)

func lineKey(l *textedit.Line, key, text string, clean func(string) string) (changed, consumed bool) {
	return lineKeyWith(l, key, text, clean, textedit.LineOpts{AllowClear: true})
}

func lineKeyWith(l *textedit.Line, key, text string, clean func(string) string, o textedit.LineOpts) (changed, consumed bool) {
	if key == "space" {
		text = " "
	}
	if text != "" && clean != nil {
		if text = clean(text); text == "" {
			return false, true
		}
	}
	return l.Edit(key, text, o)
}

func lineInsert(l *textedit.Line, s string, clean func(string) string) bool {
	if clean != nil {
		s = clean(s)
	}
	return l.Insert(s)
}

func cleanName(text string) string {
	add := printableRunes(text)
	if add == "" || combiningOnly(add) {
		return ""
	}
	return add
}

func fieldRow(l *textedit.Line, width int, bg color.Color, pal overlay.Palette) string {
	head := overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(overlay.Sigil())
	if lo, hi, ok := l.Selection(); ok {
		return head + overlay.FieldLineSelect(l.String(), l.Caret(), lo, hi, max(width-4, 1), bg, pal.Fg,
			pal.Selected, overlay.Readable(pal.Fg, pal.Selected))
	}
	before, at, after := l.Parts()
	return head + overlay.FieldLine(before, at, after, max(width-4, 1), bg, pal.Fg)
}

func cleanControl(text string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, text)
}
