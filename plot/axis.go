package plot

import "math"

// niceStep returns a round step near span/n: 1, 2 or 5 times a power of 10.
func niceStep(span float64, n int) float64 {
	if span <= 0 || !finite(span) || n < 1 {
		return 1
	}
	raw := span / float64(n)
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, m := range []float64{1, 2, 5, 10} {
		if raw <= m*mag {
			return m * mag
		}
	}
	return 10 * mag
}

// niceRange widens [lo, hi] to round numbers, about n steps apart.
func niceRange(lo, hi float64, n int) (float64, float64) {
	if !finite(lo) || !finite(hi) || hi < lo {
		return 0, 1
	}
	if hi == lo {
		if lo == 0 {
			return 0, 1
		}
		d := math.Abs(lo) / 2
		lo, hi = lo-d, hi+d
	}
	step := niceStep(hi-lo, n)
	nlo, nhi := math.Floor(lo/step)*step, math.Ceil(hi/step)*step
	if !finite(nlo) || !finite(nhi) {
		return lo, hi
	}
	return nlo, nhi
}

// axisFrame is the layout of a chart with a y axis at the left and an x
// axis under it: the plot area, and where the labels go.
type axisFrame struct {
	x0, y0, pw, ph int // the plot area
	labelW         int
}

// drawAxes draws y tick labels for lo and hi (and the middle when there are
// five rows or more), the axis line, the base line, and the x labels under
// it. It returns the plot area. With too little room it draws no axis and
// the plot area is the whole grid.
func drawAxes(g *Grid, lo, hi float64, format func(float64) string, xLabels [3]string, m Mode) axisFrame {
	if format == nil {
		format = Compact
	}
	xRow := xLabels != [3]string{}
	rows := g.H - 1
	if xRow {
		rows--
	}
	if g.W < 8 || rows < 2 {
		return axisFrame{pw: g.W, ph: g.H}
	}
	ticks := []float64{hi, lo}
	if rows >= 5 {
		ticks = []float64{hi, (lo + hi) / 2, lo}
	}
	labelW := 0
	for _, t := range ticks {
		labelW = max(labelW, width(format(t)))
	}
	labelW = min(labelW, g.W/4)
	ax := labelW + 1
	// Tick rows: the top row is hi, the bottom row is lo.
	tickRow := func(i int) int { return i * (rows - 1) / (len(ticks) - 1) }
	for y := 0; y < rows; y++ {
		g.set(ax-1, y, block(m, "│", "|"), Edge)
	}
	for i, t := range ticks {
		y := tickRow(i)
		g.TextRight(ax-1, y, truncate(format(t), labelW, ""), Mute, false)
		g.set(ax-1, y, block(m, "┤", "+"), Edge)
	}
	g.set(ax-1, rows, block(m, "└", "+"), Edge)
	for x := ax; x < g.W; x++ {
		g.set(x, rows, block(m, "─", "-"), Edge)
	}
	if xRow {
		y := rows + 1
		pw := g.W - ax
		left := truncate(xLabels[0], pw/3, ellipsis(m))
		right := truncate(xLabels[2], pw/3, ellipsis(m))
		g.Text(ax, y, left, Mute, false)
		g.TextRight(g.W, y, right, Mute, false)
		if mid := xLabels[1]; mid != "" {
			mw := width(mid)
			x := ax + (pw-mw)/2
			if x > ax+width(left)+1 && x+mw < g.W-width(right)-1 {
				g.Text(x, y, mid, Mute, false)
			}
		}
	}
	return axisFrame{x0: ax, y0: 0, pw: g.W - ax, ph: rows, labelW: labelW}
}

// sub returns a grid for the plot area, sharing nothing; paste copies it
// back.
func (a axisFrame) sub() *Grid { return NewGrid(a.pw, a.ph) }

// paste copies the cells of p that hold a glyph into g at the plot area.
func (a axisFrame) paste(g, p *Grid) {
	for y := 0; y < p.H; y++ {
		for x := 0; x < p.W; x++ {
			if c := p.Cells[y*p.W+x]; c.Text != "" || c.Bg != None {
				g.Cells[(a.y0+y)*g.W+a.x0+x] = c
			}
		}
	}
}

// LegendItem is one entry of a chart's legend.
type LegendItem struct {
	Label string
	Role  Role
}

// DrawLegend writes legend items on row y from x, each a coloured mark and
// its label, and returns the column after the last. It stops before an
// item that would pass the right edge.
func DrawLegend(g *Grid, x, y int, items []LegendItem, m Mode) int {
	for i, it := range items {
		mark := block(m, "■", asciiMarks[i%len(asciiMarks)])
		need := 2 + width(it.Label)
		if i > 0 {
			need += 2
		}
		if x+need > g.W {
			break
		}
		if i > 0 {
			x += 2
		}
		g.set(x, y, mark, it.Role)
		x = g.Text(x+2, y, it.Label, Mute, false)
	}
	return x
}

// LegendWidth returns the cells DrawLegend needs for items.
func LegendWidth(items []LegendItem) int {
	n := 0
	for i, it := range items {
		n += 2 + width(it.Label)
		if i > 0 {
			n += 2
		}
	}
	return n
}

// xLabels picks the labels under the first, middle and last of n values.
// A label the data does not have stays empty.
func xLabels(labels []string, n int) [3]string {
	var out [3]string
	at := func(i int) string {
		if i >= 0 && i < len(labels) && i < n {
			return labels[i]
		}
		return ""
	}
	out[0] = at(0)
	if n > 2 {
		out[1] = at(n / 2)
	}
	if n > 1 {
		out[2] = at(n - 1)
	}
	return out
}
