package app

import (
	"image/color"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
)

// lineKey applies one key press to a one-line field by the rules every short
// field shares (textedit.Line.Edit): the caret moves with the arrows, the word
// keys delete a word, ctrl+u clears, ctrl+k cuts to the end. Typed text goes
// through clean first, which keeps what the field may hold; a key whose text
// clean drops entirely is spent and changes nothing. clean may be nil.
func lineKey(l *textedit.Line, key, text string, clean func(string) string) (changed, consumed bool) {
	if key == "space" {
		text = " "
	}
	if text != "" && clean != nil {
		if text = clean(text); text == "" {
			return false, true
		}
	}
	return l.Edit(key, text, textedit.LineOpts{AllowClear: true})
}

// lineInsert types s at a one-line field's caret, as a paste does, after
// clean. It reports whether the text changed.
func lineInsert(l *textedit.Line, s string, clean func(string) string) bool {
	if clean != nil {
		s = clean(s)
	}
	return l.Insert(s)
}

// cleanName is the rule for a name field: only what the chrome will draw
// (printableRunes), and never a key press that is nothing but combining marks,
// which has no base to sit on and would stack an accent on the caret.
func cleanName(text string) string {
	add := printableRunes(text)
	if add == "" || combiningOnly(add) {
		return ""
	}
	return add
}

// fieldRow draws a dialog's one-line field: a blank, the sigil, and the text
// with its caret (overlay.FieldLine) in the rest of width, less one cell of
// right pad so the caret never sits against the frame.
func fieldRow(l *textedit.Line, width int, bg color.Color, pal overlay.Palette) string {
	before, at, after := l.Parts()
	return overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(overlay.Sigil()) +
		overlay.FieldLine(before, at, after, max(width-4, 1), bg, pal.Fg)
}

// cleanControl drops the control characters from typed or pasted text, for a
// field that holds an address or a path: anything printable stays.
func cleanControl(text string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, text)
}
