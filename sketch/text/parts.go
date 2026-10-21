package text

import (
	"strings"
	"unicode/utf8"
)

// Parts the drawings of state, class, ER, pie, mindmap and gantt diagrams share.

// frame draws a box's border in a role, with rounded or square corners, and
// clears what is inside it.
func frame(c *canvas, b rect, role uint8, round bool) {
	for y := b.y0; y <= b.y1; y++ {
		for x := b.x0; x <= b.x1; x++ {
			c.clear(x, y)
		}
	}
	tl, tr, bl, br := '┌', '┐', '└', '┘'
	if round {
		tl, tr, bl, br = '╭', '╮', '╰', '╯'
	}
	for x := b.x0 + 1; x < b.x1; x++ {
		c.put(x, b.y0, '─', role)
		c.put(x, b.y1, '─', role)
	}
	for y := b.y0 + 1; y < b.y1; y++ {
		c.put(b.x0, y, '│', role)
		c.put(b.x1, y, '│', role)
	}
	c.put(b.x0, b.y0, tl, role)
	c.put(b.x1, b.y0, tr, role)
	c.put(b.x0, b.y1, bl, role)
	c.put(b.x1, b.y1, br, role)
}

// rule draws a line across a box at row y, joined to its sides.
func rule(c *canvas, b rect, y int, role uint8) {
	c.put(b.x0, y, '├', role)
	for x := b.x0 + 1; x < b.x1; x++ {
		c.put(x, y, '─', role)
	}
	c.put(b.x1, y, '┤', role)
}

// centred writes a line centred between x0 and x1, ends included.
func centred(c *canvas, x0, x1, y int, s string, role uint8) {
	c.text(x0+(x1-x0+1-width(s))/2, y, s, role)
}

// wrap breaks a line at spaces so that each piece is at most w cells wide. A
// word longer than w is cut. w of 0 or less leaves the line whole.
func wrap(s string, w int) []string {
	if w <= 0 || width(s) <= w {
		return []string{s}
	}
	var out []string
	line := ""
	for _, word := range strings.Fields(s) {
		// ww is the width of what is left of the word. It goes down by the
		// width of each cut, so a long word is measured once, not once a cut.
		ww := width(word)
		for ww > w {
			if line != "" {
				out = append(out, line)
				line = ""
			}
			cut, n := 0, 0
			for i, r := range word {
				if n+runeWidth(r) > w {
					cut = i
					break
				}
				n += runeWidth(r)
			}
			if cut == 0 {
				// A wide character in one column: it goes on a line of its own.
				var r rune
				r, cut = utf8.DecodeRuneInString(word)
				n = runeWidth(r)
			}
			out = append(out, word[:cut])
			word = word[cut:]
			ww -= n
		}
		switch {
		case line == "":
			line = word
		case width(line)+1+width(word) <= w:
			line += " " + word
		default:
			out = append(out, line)
			line = word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// withTitle puts the title over a drawing, centred, with a blank line under
// it. Lines wider than cols are wrapped.
func withTitle(c *canvas, title []string, cols int) (*canvas, error) {
	var lines []string
	for _, l := range title {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, wrap(l, cols)...)
		}
	}
	if len(lines) == 0 {
		return c, nil
	}
	w := max(c.w, widest(lines))
	out, err := newCanvas(w, c.h+len(lines)+1)
	if err != nil {
		return nil, err
	}
	for k, l := range lines {
		centred(out, 0, w-1, k, l, roleTitle)
	}
	top := len(lines) + 1
	for y := 0; y < c.h; y++ {
		copy(out.cells[(top+y)*w:(top+y)*w+c.w], c.cells[y*c.w:(y+1)*c.w])
	}
	return out, nil
}
