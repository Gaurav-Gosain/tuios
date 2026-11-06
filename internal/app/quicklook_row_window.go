package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/charmbracelet/x/ansi"
)

// quickLookWindowMin is the fewest bytes a line must have before quick look
// cuts a row's window from it; a line of this many bytes or fewer is drawn
// whole. The diff view styles a whole line and then cuts it, which costs a walk
// of the line for every row drawn from it; below this length the walk that
// finds a row's window costs about what it saves. It was chosen by
// measurement: lines of 80 bytes (CJK, ASCII, mixed) drew no faster windowed,
// and CJK was about 3% slower, while lines of 300 bytes drew 6 to 11% faster.
// That was measured with the window always on. quickLookWindowed lifts the
// floor to 3*(width+1) for a wide row, and on a panel where that is larger than
// 128 the exact skip draws such lines whole anyway, so the 128 floor decides
// only on narrow panels.
const quickLookWindowMin = 128

// quickLookWindowed reports whether a line is long enough to draw a row of it
// from a window: more than max(quickLookWindowMin, 3*(width+1)) bytes, width
// being the draw width handed to Code. Every unit the walk counts takes at
// least as many bytes as the cells it is counted for (a printable ASCII byte is
// one byte and one cell; any other unit is two or more bytes and at most two
// cells), so a line with no more bytes than 3*(width+1) never has a window that
// stops before its end: the first row's window is the whole line and every
// later row reaches the line's end. The walk would cost something and change
// nothing, so such a line is drawn whole. The test is on the byte length alone.
func quickLookWindowed(line string, width int) bool {
	return len(line) > max(quickLookWindowMin, 3*(width+1))
}

// quickLookWalk finds the window of a long line that one drawn row needs, so
// the row is styled and cut from a few hundred bytes and not from the whole
// line. It walks the line in the units ansi.Truncate and ansi.TruncateLeft use
// (a printable ASCII byte is one cell, any other byte starts a grapheme
// cluster), so a cut falls where theirs would.
//
// The walk is carried from row to row of one line: the row after this one
// resumes at the unit that holds its first column, which this row's walk
// passed. A line is walked once to its last drawn cell and not once a row.
// A quickLookWalk lives for one line of one frame.
type quickLookWalk struct {
	line string
	// pos and col are a unit boundary at or before the next row's first
	// column: the byte offset and the cell column there.
	pos, col int
	// span is the first span that may reach the next row.
	span int
}

// window returns the part of the line to draw a row from: line[b0:end], to be
// drawn from column off of it, width cells wide. xOff is the row's first
// column of the line, width what the draw is given, and nextX the first column
// of the row after it (wrapping on; anything otherwise). A wide unit that
// straddles xOff is kept whole, as TruncateLeft keeps it, and off is then 1.
// The walk counts truncate units, and a unit can be counted up to two cells
// wider than its grapheme width (a printable ASCII byte followed by a skin-tone
// modifier counts 3 here and 1 in StringWidth; a halfwidth kana followed by
// U+FF9E or U+FF9F counts 2 and 1), while Code decides on an ellipsis from
// grapheme widths. So the end is where 3*(off+width+1) cells are covered: the
// window's grapheme width then stays above width whenever the line goes on, and
// the draw makes the same decision on whether the line goes on past the row,
// and so on its ellipsis, as it would on the whole line. Two things stay
// different from the whole line, because Code mixes grapheme widths with
// truncate units there: a wrapped row after many such pairs before b0, and a
// wrapped row with many such pairs inside its window. 0 <= b0 <= end <=
// len(line) always: every unit takes at least one byte.
func (w *quickLookWalk) window(xOff, width, nextX int) (b0, off, end int) {
	line := w.line
	p, c := w.pos, w.col
	b0, col0 := p, c
	have0 := xOff <= 0
	nextP, nextC, haveNext := p, c, false
	for p < len(line) {
		n, uw := 1, 1
		if b := line[p]; b < 0x20 || b > 0x7e {
			var cl string
			cl, uw = ansi.FirstGraphemeCluster(line[p:], ansi.GraphemeWidth)
			n = max(len(cl), 1)
		}
		if !have0 && c+uw > xOff {
			have0, b0, col0, off = true, p, c, xOff-c
		}
		if !haveNext && c+uw > nextX {
			haveNext, nextP, nextC = true, p, c
		}
		if have0 && c-col0 >= 3*(off+width+1) {
			break
		}
		p += n
		c += uw
	}
	if !have0 {
		// The line ends before the column: nothing of it shows.
		b0, off = p, max(xOff-c, 0)
	}
	if haveNext {
		w.pos, w.col = nextP, nextC
	} else {
		w.pos, w.col = b0, col0
	}
	return b0, off, p
}

// clip returns the spans that reach line[b0:end], cut to it and moved so that
// b0 is 0, appended to buf[:0]. Spans are in order and apart; b0 never goes
// back along one line, so spans before it are not looked at again.
func (w *quickLookWalk) clip(buf, spans []diffview.Span, b0, end int) []diffview.Span {
	buf = buf[:0]
	for w.span < len(spans) && spans[w.span].End <= b0 {
		w.span++
	}
	for i := w.span; i < len(spans) && spans[i].Start < end; i++ {
		s := diffview.Span{Start: max(spans[i].Start, b0) - b0, End: min(spans[i].End, end) - b0, Class: spans[i].Class}
		if s.Start < s.End {
			buf = append(buf, s)
		}
	}
	return buf
}
