package loom

import "strings"

// span is a run of text in one style.
type span struct {
	text string
	st   style
}

// line is one row of spans.
type line []span

func (l line) width() int {
	n := 0
	for _, s := range l {
		n += width(s.text)
	}
	return n
}

// wrapMode is how text breaks across rows.
type wrapMode uint8

const (
	wrapWord wrapMode = iota
	wrapChar
	wrapNone
)

// atom is one grapheme of a span sequence.
type atom struct {
	g  string
	w  int
	si int // the span the atom came from
}

// atomize cuts spans into graphemes, keeping newlines as atoms of width 0.
func atomize(spans []span) []atom {
	var out []atom
	for i, s := range spans {
		t := s.text
		for t != "" {
			if t[0] == '\n' {
				out = append(out, atom{g: "\n", si: i})
				t = t[1:]
				continue
			}
			if t[0] == '\t' {
				out = append(out, atom{g: " ", w: 1, si: i})
				t = t[1:]
				continue
			}
			g, w := nextGrapheme(&t)
			if w == 0 {
				continue
			}
			out = append(out, atom{g: g, w: w, si: i})
		}
	}
	return out
}

// wrap breaks spans into rows of at most w cells. With wrapNone a row
// breaks only at a newline, and may be wider than w: the painter cuts it.
func wrap(spans []span, w int, mode wrapMode) []line {
	atoms := atomize(spans)
	var rows [][]atom
	var cur []atom
	curW, lastSpace := 0, -1
	push := func(soft bool) {
		// A soft break drops the spaces at the end of the row.
		if soft {
			for len(cur) > 0 && cur[len(cur)-1].g == " " {
				cur = cur[:len(cur)-1]
			}
		}
		rows = append(rows, cur)
		cur, curW, lastSpace = nil, 0, -1
	}
	for _, a := range atoms {
		if a.g == "\n" {
			push(false)
			continue
		}
		if mode != wrapNone && w > 0 && curW+a.w > w {
			switch {
			case a.g == " ":
				push(true)
				continue
			case mode == wrapWord && lastSpace >= 0:
				rest := append([]atom(nil), cur[lastSpace+1:]...)
				cur = cur[:lastSpace]
				push(true)
				cur = rest
				for _, r := range rest {
					curW += r.w
				}
			case len(cur) > 0:
				push(false)
			}
		}
		if a.g == " " && len(cur) == 0 && len(rows) > 0 && mode != wrapNone {
			// No space at the start of a wrapped row.
			if prev := rows[len(rows)-1]; len(prev) > 0 {
				continue
			}
		}
		cur = append(cur, a)
		curW += a.w
		if a.g == " " {
			lastSpace = len(cur) - 1
		}
	}
	if len(cur) > 0 || len(rows) == 0 {
		push(false)
	}
	out := make([]line, len(rows))
	for i, r := range rows {
		out[i] = joinAtoms(r, spans)
	}
	return out
}

// joinAtoms rebuilds spans from atoms, merging runs of one span.
func joinAtoms(atoms []atom, spans []span) line {
	var out line
	var b strings.Builder
	cur := -1
	flush := func() {
		if cur >= 0 && b.Len() > 0 {
			out = append(out, span{text: b.String(), st: spans[cur].st})
		}
		b.Reset()
	}
	for _, a := range atoms {
		if a.si != cur {
			flush()
			cur = a.si
		}
		b.WriteString(a.g)
	}
	flush()
	return out
}

// textAlign is a horizontal alignment.
type textAlign uint8

const (
	alignLeft textAlign = iota
	alignCenter
	alignRight
)

// drawLine draws one row of spans into w cells from x, aligned, and cut with
// an ellipsis when it is wider.
func (c *canvas) drawLine(x, y, w int, l line, al textAlign) {
	if w <= 0 {
		return
	}
	lw := l.width()
	if lw > w {
		l = cutLine(l, w, c.g.ellipsis)
		lw = l.width()
	}
	switch al {
	case alignCenter:
		x += (w - lw) / 2
	case alignRight:
		x += w - lw
	}
	for _, s := range l {
		x = c.text(x, y, s.text, s.st, -1)
	}
}

// cutLine cuts a row to w cells, the last cell an ellipsis in the style of
// the text it replaces.
func cutLine(l line, w int, ellipsis string) line {
	ew := width(ellipsis)
	if w < ew {
		return nil
	}
	var out line
	used := 0
	for _, s := range l {
		sw := width(s.text)
		if used+sw <= w-ew {
			out = append(out, s)
			used += sw
			continue
		}
		cut := truncate(s.text, w-ew-used, "")
		out = append(out, span{text: cut + ellipsis, st: s.st})
		return out
	}
	return out
}

// plainLine returns a row of one span.
func plainLine(s string, st style) line { return line{{text: s, st: st}} }
