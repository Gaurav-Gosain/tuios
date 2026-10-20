package text

import (
	"strings"

	"tuios.dev/sketch"
)

// Pie charts: a labelled bar for each slice, in the order pele draws the
// slices, with its share and its value. The largest slice fills the bar
// column.

// eighths are the blocks that end a bar, from one eighth of a cell to a whole one.
var eighths = [...]rune{'▏', '▎', '▍', '▌', '▋', '▊', '▉', '█'}

// bar is a run of blocks n eighths of a cell long.
func bar(n int) string {
	if n <= 0 {
		return ""
	}
	s := strings.Repeat("█", n/8)
	if r := n % 8; r > 0 {
		s += string(eighths[r-1])
	}
	return s
}

func drawPie(l *sketch.Layout, opt Options) (*canvas, error) {
	p := l.Pie
	if p == nil {
		return nil, ErrUnsupported
	}
	labelW, pctW, valW := 0, 0, 0
	top := 0.0
	for _, s := range p.Slices {
		labelW = max(labelW, width(s.Label))
		pctW = max(pctW, width(s.Percent))
		valW = max(valW, width(s.Value))
		top = max(top, s.Share)
	}
	// Columns: label, bar, share, value, two cells apart.
	barW := 40
	if opt.Cols > 0 {
		labelW = min(labelW, max(opt.Cols/3, 8))
		barW = min(barW, opt.Cols-labelW-pctW-valW-6)
		if barW < 4 {
			return nil, ErrTooWide
		}
	}
	type row struct {
		label []string
		n     int
	}
	rows := make([]row, len(p.Slices))
	h := 0
	for i, s := range p.Slices {
		rows[i].label = wrap(s.Label, labelW)
		labelW = max(labelW, widest(rows[i].label))
		if top > 0 {
			rows[i].n = int(s.Share/top*float64(barW*8) + 0.5)
		}
		h += len(rows[i].label)
	}
	w := labelW + barW + pctW + valW + 6
	c, err := newCanvas(w, max(h, 1))
	if err != nil {
		return nil, err
	}
	y := 0
	for i, s := range p.Slices {
		r := rows[i]
		for k, line := range r.label {
			c.text(0, y+k, line, roleText)
		}
		x := labelW + 2
		c.text(x, y, bar(r.n), roleBox)
		x += barW + 2
		c.text(x+pctW-width(s.Percent), y, s.Percent, roleTitle)
		x += pctW + 2
		c.text(x+valW-width(s.Value), y, s.Value, roleLine)
		y += len(r.label)
	}
	return withTitle(c, p.Title, opt.Cols)
}
