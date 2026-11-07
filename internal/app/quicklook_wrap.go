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
