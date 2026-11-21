package app

import (
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/diffview"
)

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

const quickLookCutMargin = 3

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
