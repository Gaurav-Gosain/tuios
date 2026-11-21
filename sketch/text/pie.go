package text

import (
	"strings"

	"tuios.dev/plot"
	sketch "tuios.dev/sketch/internal/engine"
	"tuios.dev/sketch/internal/types"
)

const pieAspect = 2.1

const pieMaxSlices = 12

var eighths = [...]rune{'▏', '▎', '▍', '▌', '▋', '▊', '▉', '█'}

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
	if opt.Color && (opt.Cols == 0 || opt.Cols > 40) && len(p.Slices) <= pieMaxSlices {
		if c, err := pieDisc(p, opt); err != ErrTooWide {
			return c, err
		}
	}
	return pieList(p, opt)
}

func drawn(s types.GeoSlice) float64 {
	if s.Share*100 >= 1 && s.Percent != "0%" {
		return s.Share
	}
	return 0
}

func pieOf(p *types.GeoPie) plot.Pie {
	vals := make([]float64, len(p.Slices))
	for i, s := range p.Slices {
		vals[i] = drawn(s)
	}
	return plot.Pie{Values: vals, NoLegend: true, Aspect: pieAspect}
}

func pieColumns(p *types.GeoPie) (label, pct, value int) {
	for _, s := range p.Slices {
		label = max(label, width(s.Label))
		pct = max(pct, width(s.Percent))
		if p.ShowData {
			value = max(value, width(s.Value))
		}
	}
	return label, pct, value
}

func (c *canvas) shareAndValue(x, y int, s types.GeoSlice, pctW, valW int) {
	c.text(x+pctW-width(s.Percent), y, s.Percent, roleTitle)
	if valW > 0 {
		x += pctW + 2
		c.text(x+valW-width(s.Value), y, s.Value, roleLine)
	}
}

func pieDisc(p *types.GeoPie, opt Options) (*canvas, error) {
	pie := pieOf(p)
	some := false
	for _, v := range pie.Values {
		some = some || v > 0
	}
	if !some {
		return nil, ErrTooWide
	}
	labelW, pctW, valW := pieColumns(p)
	labelW = min(labelW, 32)
	legend := func(lw int) int {
		w := 2 + lw + 2 + pctW
		if valW > 0 {
			w += 2 + valW
		}
		return w
	}
	rows := 12
	if opt.Cols == 0 || opt.Cols >= 110 {
		rows = 14
	}
	const gap = 4
	cols := func(r int) int { return int(float64(r) * pieAspect) }
	if opt.Cols > 0 {
		for rows > 8 && cols(rows)+gap+legend(labelW) > opt.Cols {
			rows--
		}
		if over := cols(rows) + gap + legend(labelW) - opt.Cols; over > 0 {
			if labelW-over < min(labelW, 8) {
				return nil, ErrTooWide
			}
			labelW -= over
		}
	}
	dc := cols(rows)
	g := pie.Draw(dc, rows, plot.Blocks)
	n := len(p.Slices)
	h := max(rows, n)
	c, err := newCanvas(dc+gap+legend(labelW), h)
	if err != nil {
		return nil, err
	}
	y0 := (h - rows) / 2
	for y := 0; y < g.H; y++ {
		for x := 0; x < g.W; x++ {
			cell := g.At(x, y)
			if cell.Text == "" || cell.Text == " " {
				continue
			}
			bg := uint8(roleNone)
			if cell.Bg != plot.None {
				bg = canvasRole(cell.Bg)
			}
			c.putBg(x, y0+y, []rune(cell.Text)[0], canvasRole(cell.Role), bg)
		}
	}
	lx := dc + gap
	ly := (h - n) / 2
	for i, s := range p.Slices {
		mark, role := pie.Mark(i, plot.Blocks)
		c.put(lx, ly+i, []rune(mark)[0], canvasRole(role))
		c.text(lx+2, ly+i, fit(s.Label, labelW), roleText)
		c.shareAndValue(lx+2+labelW+2, ly+i, s, pctW, valW)
	}
	return withTitle(c, p.Title, opt.Cols)
}

func pieList(p *types.GeoPie, opt Options) (*canvas, error) {
	pie := pieOf(p)
	labelW, pctW, valW := pieColumns(p)
	top := 0.0
	for _, s := range p.Slices {
		top = max(top, s.Share)
	}
	right := pctW
	if valW > 0 {
		right += 2 + valW
	}
	barW := 40
	if opt.Cols > 0 {
		labelW = min(labelW, max(opt.Cols/3, 8))
		barW = min(barW, opt.Cols-labelW-right-4)
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
		if i > 0 {
			h++
		}
		h += len(rows[i].label)
	}
	c, err := newCanvas(labelW+2+barW+2+right, max(h, 1))
	if err != nil {
		return nil, err
	}
	y := 0
	for i, s := range p.Slices {
		r := rows[i]
		for k, line := range r.label {
			c.text(0, y+k, line, roleText)
		}
		_, role := pie.Mark(i, plot.Blocks)
		c.text(labelW+2, y, bar(r.n), canvasRole(role))
		c.shareAndValue(labelW+2+barW+2, y, s, pctW, valW)
		y += len(r.label) + 1
	}
	return withTitle(c, p.Title, opt.Cols)
}
