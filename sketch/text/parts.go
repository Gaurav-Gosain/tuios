package text

import (
	"strings"
	"unicode/utf8"
)

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

func rule(c *canvas, b rect, y int, role uint8) {
	c.put(b.x0, y, '├', role)
	for x := b.x0 + 1; x < b.x1; x++ {
		c.put(x, y, '─', role)
	}
	c.put(b.x1, y, '┤', role)
}

func centred(c *canvas, x0, x1, y int, s string, role uint8) {
	c.text(x0+(x1-x0+1-width(s))/2, y, s, role)
}

func wrap(s string, w int) []string {
	if w <= 0 || width(s) <= w {
		return []string{s}
	}
	var out []string
	line := ""
	for _, field := range strings.Fields(s) {
		pieces := hintPieces(field)
		rest := width(field)
		for k, word := range pieces {
			glue := ""
			if k == 0 && line != "" {
				glue = " "
			}
			ww := width(word)
			fits := width(line)+len(glue)+ww <= w
			if strings.HasSuffix(word, "\u00ad") && k < len(pieces)-1 {
				fits = width(line)+len(glue)+ww+1 <= w || width(line)+len(glue)+rest <= w
			}
			rest -= ww
			if line != "" && !fits {
				out = append(out, broken(line))
				line, glue = "", ""
			}
			for ww > w {
				if line != "" {
					out = append(out, broken(line))
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
					var r rune
					r, cut = utf8.DecodeRuneInString(word)
					n = runeWidth(r)
				}
				out = append(out, word[:cut])
				word = word[cut:]
				ww -= n
			}
			line += glue + word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

func hintPieces(word string) []string {
	if !strings.ContainsRune(word, 0x200b) && !strings.ContainsRune(word, 0xad) {
		return []string{word}
	}
	var out []string
	from := 0
	for i, r := range word {
		if isHint(r) {
			end := i + utf8.RuneLen(r)
			out = append(out, word[from:end])
			from = end
		}
	}
	if from < len(word) {
		out = append(out, word[from:])
	}
	return out
}

func broken(line string) string {
	if strings.HasSuffix(line, "\u00ad") {
		return strings.TrimSuffix(line, "\u00ad") + "-"
	}
	return line
}

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
