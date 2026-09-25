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
	if width <= 0 {
		return ""
	}
	c := b2i(cursor)
	codes := &t.code[kind][c]
	var b strings.Builder
	b.Grow(len(text) + 32*(len(spans)+2))
	si := 0
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
		if !changed.Empty() {
			switch {
			case i < changed.Start:
				next = min(next, changed.Start)
			case i < changed.End:
				word = true
				next = min(next, changed.End)
			}
		}
		next = max(min(next, len(text)), i+1)
		b.WriteString(codes[b2i(word)][class])
		b.WriteString(text[i:next])
		i = next
	}
	plain := codes[0][Plain]
	styled := b.String()
	left := ansi.StringWidth(text) - xOff
	if xOff > 0 && left > 0 {
		styled = ansi.TruncateLeft(styled, xOff, "")
	}
	ell := overlay.Ellipsis()
	ellW := ansi.StringWidth(ell)
	switch {
	case left <= 0:
		styled = ""
	case left > width && width > ellW:
		styled = ansi.Truncate(styled, width-ellW, "") + plain + ell
	case left > width:
		styled = ansi.Truncate(styled, width, "")
	}
	if w := ansi.StringWidth(styled); w < width {
		styled += plain + strings.Repeat(" ", width-w)
	}
	return styled + reset
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
