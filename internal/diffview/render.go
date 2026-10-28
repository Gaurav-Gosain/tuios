package diffview

import (
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/x/ansi"
)

// reset ends every piece the view draws, so nothing it set leaks into what
// the caller draws next.
const reset = "\x1b[m"

// Code draws a line's text in exactly width cells, from column xOff of the
// text on. spans colour it and changed marks the part that changed; both are
// in bytes of text. A line longer than the room ends in an ellipsis. That a
// line is scrolled sideways is Sign's to show, so no cell of code is given
// up for it.
//
// text has to be printable already: the view draws it as it is.
func (t *Theme) Code(text string, spans []Span, changed Range, kind Kind, cursor bool, xOff, width int) string {
	one := [1]Range{changed}
	return t.CodeRanges(text, spans, one[:], kind, cursor, xOff, width)
}

// CodeRanges is Code with more than one changed part, in order and apart:
// each is lit, and the text between them is not.
func (t *Theme) CodeRanges(text string, spans []Span, changed []Range, kind Kind, cursor bool, xOff, width int) string {
	if width <= 0 {
		return ""
	}
	c := b2i(cursor)
	codes := &t.code[kind][c]
	var b strings.Builder
	b.Grow(len(text) + 32*(len(spans)+2))
	si, ri := 0, 0
	for i := 0; i < len(text); {
		for si < len(spans) && spans[si].End <= i {
			si++
		}
		class, next := Plain, len(text)
		if si < len(spans) {
			if spans[si].Start <= i {
				class, next = spans[si].Class, spans[si].End
			} else {
				next = spans[si].Start
			}
		}
		word := false
		for ri < len(changed) && (changed[ri].End <= i || changed[ri].Empty()) {
			ri++
		}
		if ri < len(changed) {
			if i < changed[ri].Start {
				next = min(next, changed[ri].Start)
			} else {
				word = true
				next = min(next, changed[ri].End)
			}
		}
		next = max(min(next, len(text)), i+1)
		b.WriteString(codes[b2i(word)][class])
		b.WriteString(text[i:next])
		i = next
	}
	plain := codes[0][Plain]
	styled := b.String()
	// Printable ASCII is one cell a byte, so the widths are known without
	// walking the text; the styled result's follows from the branch taken.
	ascii := xOff >= 0 && printableASCII(text)
	textW := len(text)
	if !ascii {
		textW = ansi.StringWidth(text)
	}
	left := textW - xOff
	if xOff > 0 && left > 0 {
		styled = ansi.TruncateLeft(styled, xOff, "")
	}
	ell := overlay.Ellipsis()
	ellW := ansi.StringWidth(ell)
	known := left
	switch {
	case left <= 0:
		styled = ""
		known = 0
	case left > width && width > ellW:
		styled = ansi.Truncate(styled, width-ellW, "") + plain + ell
		known = width
	case left > width:
		styled = ansi.Truncate(styled, width, "")
		known = width
	}
	w := known
	if !ascii {
		w = ansi.StringWidth(styled)
	}
	if w < width {
		styled += plain + strings.Repeat(" ", width-w)
	}
	return styled + reset
}

// printableASCII reports whether s holds only bytes 0x20 to 0x7E, each one
// cell wide.
func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// Gutter draws a line number right-aligned in width cells, with a space on
// its right. n of zero or less leaves the cells blank, for the side of a
// line that has no number there.
func (t *Theme) Gutter(n, width int, kind Kind, cursor bool) string {
	if width <= 0 {
		return ""
	}
	s := ""
	if n > 0 {
		s = strconv.Itoa(n)
	}
	s += " "
	if len(s) > width {
		s = s[len(s)-width:]
	}
	return t.gutter[kind][b2i(cursor)] + strings.Repeat(" ", width-len(s)) + s + reset
}

// SignWidth is how many cells Sign draws.
const SignWidth = 2

// Sign draws the mark before a line's code: "+" for an added line, "-" for
// a removed one, blank otherwise, then an ellipsis when the code is scrolled
// sideways and there is code to its left that does not show.
func (t *Theme) Sign(kind Kind, cursor, scrolled bool) string {
	mark := " "
	switch kind {
	case Add:
		mark = "+"
	case Delete:
		mark = "-"
	}
	after := " "
	if scrolled {
		after = overlay.Ellipsis()
		if ansi.StringWidth(after) != 1 {
			after = "<"
		}
	}
	return t.sign[kind][b2i(cursor)] + mark + after + reset
}

// Blank draws width empty cells on a line's code ground.
func (t *Theme) Blank(kind Kind, cursor bool, width int) string {
	if width <= 0 {
		return ""
	}
	return t.code[kind][b2i(cursor)][0][Plain] + strings.Repeat(" ", width) + reset
}

// BlankGutter draws width empty cells on a line's number column ground.
func (t *Theme) BlankGutter(kind Kind, cursor bool, width int) string {
	if width <= 0 {
		return ""
	}
	return t.gutter[kind][b2i(cursor)] + strings.Repeat(" ", width) + reset
}
