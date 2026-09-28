package app

import (
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/hints"
)

// findMatches runs the matcher over the copied view and labels what it
// finds.
//
// The view is read as lines rather than rows. A row the emulator wrapped onto
// the next one is joined to it, so a URL the pane broke across two rows is one
// line of text to the matcher and one match on the screen. A line that only
// happens to fill the row is not joined. Each
// cell's text goes into the line once, so a wide glyph is one character there
// and two columns on the screen, and every byte of the line knows which cell
// drew it.
func (s *hintsState) findMatches(matcher *hints.Matcher, cursor hintCell) {
	s.matches = nil
	var targets []hints.Target
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
			s.matches = append(s.matches, hintMatch{
				text:  text[found.Start:found.End],
				kind:  found.Kind,
				cells: cells,
			})
			targets = append(targets, hints.Target{
				Text:     text[found.Start:found.End],
				Distance: hintDistance(cells[0], cursor, s.w),
			})
		}
		y = next
	}

	labels := hints.Assign(targets, s.alphabet)
	s.owner = make([]int, s.w*s.h)
	for i := range s.owner {
		s.owner[i] = -1
	}
	s.labelRune = make(map[int]rune)
	s.labelOf = make(map[int]int)
	for i := range s.matches {
		match := &s.matches[i]
		match.label = labels[i]
		for _, c := range match.cells {
			s.owner[c.y*s.w+c.x] = i
		}
		// The label covers the start of the match, as it does in
		// tmux-fingers and kitty. At the end of a row with too little room it
		// moves left, so it is never cut off.
		start := match.cells[0]
		n := len(match.label)
		if start.x+n > s.w {
			start.x = max(s.w-n, 0)
		}
		match.labelAt = start
		for j, r := range match.label {
			x := start.x + j
			if x >= s.w {
				break
			}
			s.labelRune[start.y*s.w+x] = r
			s.labelOf[start.y*s.w+x] = i
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
