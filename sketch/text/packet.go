package text

import (
	"math"
	"strconv"
	"strings"

	"tuios.dev/sketch"
)

// A packet is drawn as its rows of bit fields: each field a box as wide as its bits, the
// boxes of a row side by side, with the bit numbers above them as in the SVG. A label that
// does not fit its box wraps, and a word too long for it is cut. A row of bits that does not
// fit the columns gives ErrTooWide, and Draw asks for the narrow layout, which has fewer
// bits to a row.

// wrapCells breaks s into lines of at most w cells, at spaces, cutting a word longer than
// a line. At most most lines are kept; the last one kept ends in … when text is left.
func wrapCells(s string, w, most int) []string {
	if w < 1 || most < 1 {
		return nil
	}
	var lines []string
	var line strings.Builder
	lw := 0
	flush := func() {
		lines = append(lines, line.String())
		line.Reset()
		lw = 0
	}
	for _, word := range strings.Fields(s) {
		ww := width(word)
		for ww > 0 {
			if lw > 0 && lw+1+ww <= w {
				line.WriteByte(' ')
				line.WriteString(word)
				lw += 1 + ww
				break
			}
			if lw > 0 {
				flush()
			}
			if ww <= w {
				line.WriteString(word)
				lw = ww
				break
			}
			// Cut the word at w cells.
			cut, n := 0, 0
			for i, r := range word {
				if n+runeWidth(r) > w {
					cut = i
					break
				}
				n += runeWidth(r)
			}
			if cut == 0 {
				return lines
			}
			line.WriteString(word[:cut])
			word = word[cut:]
			ww -= n
			flush()
		}
	}
	if lw > 0 {
		flush()
	}
	if len(lines) > most {
		lines = lines[:most]
		last := []rune(lines[most-1])
		for len(last) > 0 && width(string(last))+1 > w {
			last = last[:len(last)-1]
		}
		lines[most-1] = string(last) + "…"
	}
	return lines
}

func drawPacket(l *sketch.Layout, opt Options) (*canvas, error) {
	p := l.Packet
	if p == nil {
		return nil, ErrUnsupported
	}
	bpr := max(p.BitsPerRow, 1)
	rowCount := 0
	for _, f := range p.Fields {
		rowCount = max(rowCount, f.Row+1)
	}
	if rowCount > 20_000 {
		return nil, ErrTooLarge
	}
	// Cells to a bit: enough for every label on one line, at most 6, within the columns.
	// A label that does not fit wraps.
	per := 1
	for _, f := range p.Fields {
		bits := int(math.Min(f.End-f.Start+1, float64(bpr)))
		need := (width(f.Label) + 3 + bits - 1) / max(bits, 1)
		per = max(per, min(need, 6))
	}
	if opt.Cols > 0 {
		if bpr+1 > opt.Cols {
			return nil, ErrTooWide
		}
		per = min(per, (opt.Cols-1)/bpr)
	} else {
		// Without a bound, a row stays within 120 columns unless one cell to a bit is wider.
		per = max(1, min(per, 119/bpr))
	}
	// Each field's place in its row and its lines of text.
	type field struct {
		x0, x1 int
		lines  []string
		lead   string
		trail  string
	}
	rows := make([][]field, rowCount)
	heights := make([]int, rowCount)
	for _, f := range p.Fields {
		count := int(f.End - f.Start + 1)
		first := int(math.Mod(f.Start, float64(bpr)))
		at := first
		if p.Descending {
			at = bpr - first - count
		}
		fd := field{x0: at * per, x1: (at + count) * per}
		fd.lines = wrapCells(f.Label, fd.x1-fd.x0-1, 6)
		lead, trail := f.Start, f.End
		if p.Descending {
			lead, trail = f.End, f.Start
		}
		fd.lead = strconv.FormatFloat(lead, 'f', -1, 64)
		if count > 1 {
			fd.trail = strconv.FormatFloat(trail, 'f', -1, 64)
		}
		rows[f.Row] = append(rows[f.Row], fd)
		heights[f.Row] = max(heights[f.Row], len(fd.lines))
	}
	numbers := 0
	if p.ShowBits {
		numbers = 1
	}
	w := bpr*per + 1
	h := 0
	for _, n := range heights {
		h += numbers + max(n, 1) + 2
	}
	titleH := 0
	if len(p.Title) > 0 {
		titleH = len(p.Title) + 1
	}
	w = max(w, widest(p.Title))
	if opt.Cols > 0 && w > opt.Cols {
		return nil, ErrTooWide
	}
	c, err := newCanvas(w, max(h+titleH, 1))
	if err != nil {
		return nil, err
	}
	y := 0
	for r, row := range rows {
		inner := max(heights[r], 1)
		if numbers > 0 {
			// Numbers left to right, each kept clear of the one before it.
			end := -2
			put := func(x int, s string) {
				if x > end+1 && x >= 0 && x+width(s) <= w {
					c.text(x, y, s, roleNote)
					end = x + width(s) - 1
				}
			}
			for _, fd := range row {
				first, last := fd.lead, fd.trail
				if fd.x1-fd.x0 <= per && last == "" {
					put((fd.x0+fd.x1-width(first)+1)/2, first)
					continue
				}
				put(fd.x0, first)
				if last != "" {
					put(fd.x1-width(last)+1, last)
				}
			}
			y++
		}
		for _, fd := range row {
			c.box(fd.x0, y, fd.x1-fd.x0+1, inner+2, nil, roleBox, roleText)
			top := y + 1 + (inner-len(fd.lines))/2
			for k, line := range fd.lines {
				c.text(fd.x0+1+(fd.x1-fd.x0-1-width(line))/2, top+k, line, roleText)
			}
		}
		y += inner + 2
	}
	for i, line := range p.Title {
		c.text((w-width(line))/2, y+1+i, line, roleTitle)
	}
	return c, nil
}
