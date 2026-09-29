package app

import (
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/hints"
)

// find runs the matcher over the copied view and returns what it finds, in
// reading order, with no labels yet.
//
// The view is read as lines rather than rows. A row the emulator wrapped onto
// the next one is joined to it, so a URL the pane broke across two rows is one
// line of text to the matcher and one match on the screen. A line that only
// happens to fill the row is not joined. Each
// cell's text goes into the line once, so a wide glyph is one character there
// and two columns on the screen, and every byte of the line knows which cell
// drew it.
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
			// offs rises with the line, so the match's first cell is a
			// binary search away and its cells follow it in order.
			var cells []hintCell
			first, _ := slices.BinarySearch(offs, found.Start)
			for i := first; i < len(offs) && offs[i] < found.End; i++ {
				cells = append(cells, refs[i])
			}
			if len(cells) == 0 {
				continue
			}
			out = append(out, hintMatch{
				text:  text[found.Start:found.End],
				kind:  found.Kind,
				cells: cells,
			})
		}
		y = next
	}
	return out
}

// label gives every match its label, one target per match in the same
// order, and marks on each pane the cells the matches and labels cover.
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
		// The label covers the start of the match, as it does in
		// tmux-fingers and kitty. At the end of a row with too little room it
		// moves left, so it is never cut off.
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

// hintDistance is how far a match is from the cursor, rows first: a match on
// the cursor's row is nearer than any match one row away.
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
