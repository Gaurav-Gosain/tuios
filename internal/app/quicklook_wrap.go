package app

import (
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/diffview"
)

// quickLookWrapNext is where the wrapped row that starts at byte from ends: the
// most whole grapheme clusters that fit in width cells, and at least one, so a
// cluster wider than the row still moves the walk on. cells is what the row
// takes, counted in the widths the canvas draws.
func quickLookWrapNext(line string, from, width int) (to, cells int) {
	to = from
	for to < len(line) {
		var n, w int
		if b := line[to]; b >= 0x20 && b < 0x7f && (to+1 == len(line) || line[to+1] < 0x80) {
			n, w = 1, 1
		} else {
			var g string
			g, w = ansi.FirstGraphemeCluster(line[to:], ansi.GraphemeWidth)
			if g == "" {
				g, w = line[to:to+1], 0
			}
			n = len(g)
		}
		if to > from && cells+w > width {
			break
		}
		to += n
		cells += w
	}
	return to, cells
}

// quickLookCutMargin is how many times one cell more than the row quickLookCutEnd
// keeps of a line.
const quickLookCutMargin = 3

// quickLookCutEnd is where a long line can be cut before it is drawn on one row
// with wrap off: the byte after the unit that brings the count to three times
// one cell more than the row, 3*(width+1) cells, or past it, and len(line) when
// the line ends first. The row then draws the same cells as the whole line
// would, without measuring and cutting every byte of a line that is far wider
// than the row.
//
// The walk counts the units ansi.Truncate counts: a byte from 0x20 to 0x7E is one
// byte and one cell with no look-ahead, and any other byte starts one grapheme
// cluster, taking its bytes and its width (an empty cluster takes one byte and
// no cell, so the walk always moves on). That is not quickLookWrapNext's rule.
// A unit can be counted up to two cells wider than its grapheme is wide: 'q'
// followed by U+16FF0 is 3 cells to the walk and 1 to ansi.StringWidth. Code
// decides on its ellipsis from the grapheme widths of the text it is given, so
// a prefix counted at three times one cell more than the row is still at least
// width+1 cells wide to it, and the ellipsis stays whenever the line goes on.
//
// A line of no more than 3*(width+1) bytes is returned whole at once: every unit
// takes at least as many bytes as the cells the walk counts for it, so the walk
// would reach its end before the limit. A line whose units are mostly
// zero-width reaches its end before the limit too, and is returned whole.
func quickLookCutEnd(line string, width int) int {
	limit := quickLookCutMargin * (width + 1)
	if len(line) <= limit {
		return len(line)
	}
	p, cells := 0, 0
	for p < len(line) {
		if b := line[p]; b >= 0x20 && b < 0x7f {
			p++
			cells++
		} else {
			g, w := ansi.FirstGraphemeCluster(line[p:], ansi.GraphemeWidth)
			p += max(len(g), 1)
			cells += w
		}
		if cells >= limit {
			return p
		}
	}
	return len(line)
}

// quickLookRowSpans clips the syntax spans to the bytes [from, to) of a line and
// moves them to start at 0, into dst, which it returns.
func quickLookRowSpans(dst, spans []diffview.Span, from, to int) []diffview.Span {
	dst = dst[:0]
	for _, s := range spans {
		if s.End <= from {
			continue
		}
		if s.Start >= to {
			break
		}
		dst = append(dst, diffview.Span{Start: max(s.Start, from) - from, End: min(s.End, to) - from, Class: s.Class})
	}
	return dst
}
