package app

import (
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/hints"
)

func (s *hintsPane) find(matcher *hints.Matcher) []hintMatch {
	var out []hintMatch
	var b strings.Builder
	var refs []hintCell
	var offs []int
	for y := 0; y < s.h; {
		b.Reset()
		refs, offs = refs[:0], offs[:0]
		next := y
		for rows := 0; next < s.h; {
			for x := range s.w {
				c := s.cells[next][x]
				if c.Content == "" && c.Width == 0 {
					continue
				}
				offs = append(offs, b.Len())
				refs = append(refs, hintCell{x: x, y: next})
				b.WriteString(c.Content)
			}
			rows++
			full := s.rowWraps(next)
			next++
			if !full || rows >= hintsWrapRows {
				break
			}
		}
		text := b.String()
		for _, found := range matcher.Find(text) {
			var cells []hintCell
			first, _ := slices.BinarySearch(offs, found.Start)
			for i := first; i < len(offs) && offs[i] < found.End; i++ {
				cells = append(cells, refs[i])
			}
			if len(cells) == 0 {
				continue
			}
			out = append(out, hintMatch{
				text:   text[found.Start:found.End],
				kind:   found.Kind,
				target: found.Target,
				cells:  cells,
			})
		}
		y = next
	}
	return out
}

func (s *hintsState) label(targets []hints.Target) {
	labels := hints.Assign(targets, s.alphabet)
	for _, p := range s.panes {
		p.owner = make([]int, p.w*p.h)
		for i := range p.owner {
			p.owner[i] = -1
		}
		p.labelRune = make(map[int]rune)
		p.labelOf = make(map[int]int)
	}
	for i := range s.matches {
		match := &s.matches[i]
		p := s.panes[match.pane]
		match.label = labels[i]
		for _, c := range match.cells {
			p.owner[c.y*p.w+c.x] = i
		}
		start := match.cells[0]
		n := len(match.label)
		if start.x+n > p.w {
			start.x = max(p.w-n, 0)
		}
		match.labelAt = start
		for j, r := range match.label {
			x := start.x + j
			if x >= p.w {
				break
			}
			p.labelRune[start.y*p.w+x] = r
			p.labelOf[start.y*p.w+x] = i
		}
	}
}

func hintDistance(c, cursor hintCell, width int) int {
	dy := c.y - cursor.y
	if dy < 0 {
		dy = -dy
	}
	dx := c.x - cursor.x
	if dx < 0 {
		dx = -dx
	}
	return dy*(width+1) + dx
}
