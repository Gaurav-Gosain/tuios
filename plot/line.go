package plot

import (
	"math"
)

// Series is one line of a Line chart.
type Series struct {
	Name   string
	Values []float64 // evenly spaced; a missing value breaks the line
	Role   Role      // SeriesRole(i) when empty
}

// Line is a line chart, or an area chart when Fill is set. The values of
// each series are evenly spaced across the width.
type Line struct {
	Series []Series
	Fill   bool // fill below the line: an area chart
	// Axis draws the range at the left and a base line at the bottom.
	Axis bool
	// Min and Max fix the range. Both 0 means the range of the values, with
	// 0 included for an area.
	Min, Max float64
	Format   func(float64) string
}

// asciiMarks tell series apart where colour may not: in ASCII and at 16
// colours.
var asciiMarks = [...]string{"*", "+", "o", "x", "%", "@"}

func (l Line) rng() (lo, hi float64, ok bool) {
	if (l.Min != 0 || l.Max != 0) && finite(l.Min) && finite(l.Max) && l.Max > l.Min {
		return l.Min, l.Max, true
	}
	all := make([][]float64, len(l.Series))
	for i, s := range l.Series {
		all[i] = s.Values
	}
	lo, hi, ok = extent(all...)
	if !ok {
		return 0, 0, false
	}
	if l.Fill {
		lo = math.Min(lo, 0)
	}
	if hi == lo {
		lo, hi = lo-1, hi+1
	}
	return lo, hi, true
}

// Draw draws the chart into a w by h grid.
func (l Line) Draw(w, h int, m Mode) *Grid {
	g := NewGrid(w, h)
	if g.W == 0 || g.H == 0 {
		return g
	}
	lo, hi, ok := l.rng()
	if !ok {
		return g
	}
	x0, pw, ph := 0, g.W, g.H
	if l.Axis && g.W >= 6 && g.H >= 2 {
		format := l.Format
		if format == nil {
			format = Compact
		}
		top, bot := format(hi), format(lo)
		lw := min(max(Width(top), Width(bot)), g.W/3)
		g.TextRight(lw, 0, Truncate(top, lw, ""), Mute, false)
		g.TextRight(lw, g.H-2, Truncate(bot, lw, ""), Mute, false)
		ph = g.H - 1
		for y := 0; y < ph; y++ {
			g.set(lw, y, block(m, "│", "|"), Edge)
		}
		g.set(lw, ph, block(m, "└", "+"), Edge)
		for x := lw + 1; x < g.W; x++ {
			g.set(x, ph, block(m, "─", "-"), Edge)
		}
		x0 = lw + 1
		pw = g.W - x0
	}
	if pw <= 0 || ph <= 0 {
		return g
	}
	area := &Grid{W: pw, H: ph, Cells: make([]Cell, pw*ph)}
	for i := range area.Cells {
		area.Cells[i].Width = 1
	}
	switch m {
	case Braille:
		l.braille(area, lo, hi)
	default:
		l.columns(area, lo, hi, m)
	}
	for y := 0; y < ph; y++ {
		for x := 0; x < pw; x++ {
			if c := area.Cells[y*pw+x]; c.Text != "" {
				g.Cells[y*g.W+x0+x] = c
			}
		}
	}
	return g
}

func (l Line) seriesRole(i int) Role {
	if r := l.Series[i].Role; r != None {
		return r
	}
	return SeriesRole(i)
}

// points spreads a series across n columns: a value per column, NaN where
// the series has a gap. More values than columns are downsampled. Fewer
// values are joined by straight lines between neighbours.
func points(vals []float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = math.NaN()
	}
	if len(vals) == 0 || n == 0 {
		return out
	}
	if len(vals) > n {
		for _, v := range vals {
			if !finite(v) {
				return buckets(vals, n)
			}
		}
		return Downsample(vals, n)
	}
	if len(vals) == 1 || n == 1 {
		out[0] = vals[0]
		return out
	}
	for x := range out {
		t := float64(x) * float64(len(vals)-1) / float64(n-1)
		i := int(t)
		f := t - float64(i)
		switch {
		case i >= len(vals)-1:
			out[x] = vals[len(vals)-1]
		case f == 0:
			out[x] = vals[i]
		case finite(vals[i]) && finite(vals[i+1]):
			out[x] = vals[i] + (vals[i+1]-vals[i])*f
		}
	}
	return out
}

// braille draws with a 2 by 4 dot grid per cell, joining points with lines.
func (l Line) braille(g *Grid, lo, hi float64) {
	dw, dh := g.W*2, g.H*4
	dots := make([]uint8, g.W*g.H)
	owner := make([]int8, g.W*g.H)
	for i := range owner {
		owner[i] = -1
	}
	setDot := func(x, y, s int) {
		if x < 0 || y < 0 || x >= dw || y >= dh {
			return
		}
		cx, cy := x/2, y/4
		dots[cy*g.W+cx] |= brailleBit(x%2, y%4)
		owner[cy*g.W+cx] = int8(s % 127)
	}
	for s := range l.Series {
		pts := points(l.Series[s].Values, dw)
		// Lines join points that exist, across the empty columns between
		// them when the values were fewer than the columns.
		prevX, prevY := -1, 0
		for x, v := range pts {
			if !finite(v) {
				prevX = -1
				continue
			}
			y := dh - 1 - int(math.Round(unit(v, lo, hi)*float64(dh-1)))
			if prevX >= 0 {
				line(prevX, prevY, x, y, func(px, py int) { setDot(px, py, s) })
			} else {
				setDot(x, y, s)
			}
			if l.Fill {
				// Fill every column from the previous point to this one.
				from := x
				if prevX >= 0 {
					from = prevX
				}
				for fx := from; fx <= x; fx++ {
					fy := y
					if prevX >= 0 && x != prevX {
						fy = prevY + (y-prevY)*(fx-prevX)/(x-prevX)
					}
					for yy := fy; yy < dh; yy++ {
						setDot(fx, yy, s)
					}
				}
			}
			prevX, prevY = x, y
		}
	}
	for i, d := range dots {
		if d != 0 {
			g.Cells[i] = Cell{Text: string(rune(0x2800 + int(d))), Width: 1, Role: l.seriesRole(int(owner[i]))}
		}
	}
}

// brailleBit is the dot bit for column x (0 or 1) and row y (0 to 3) of a
// braille cell.
func brailleBit(x, y int) uint8 {
	if y == 3 {
		return [2]uint8{0x40, 0x80}[x]
	}
	return uint8(1) << uint(y+3*x)
}

// line calls plot for each point of a line from x0,y0 to x1,y1.
func line(x0, y0, x1, y1 int, plot func(x, y int)) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	e := dx + dy
	for n := 0; n < 1<<16; n++ {
		plot(x0, y0)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * e
		if e2 >= dy {
			e += dy
			x0 += sx
		}
		if e2 <= dx {
			e += dx
			y0 += sy
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// columns draws one level per column: eighth blocks in Blocks mode, a mark
// in ASCII. An area fills below.
func (l Line) columns(g *Grid, lo, hi float64, m Mode) {
	for s := range l.Series {
		pts := points(l.Series[s].Values, g.W)
		role := l.seriesRole(s)
		for x, v := range pts {
			if !finite(v) {
				continue
			}
			units := int(math.Round(unit(v, lo, hi) * float64(g.H*8-1)))
			row := g.H - 1 - units/8
			lv := units%8 + 1
			if m == ASCII {
				mark := asciiMarks[s%len(asciiMarks)]
				g.set(x, row, mark, role)
				if l.Fill {
					for y := row + 1; y < g.H; y++ {
						g.set(x, y, ":", role)
					}
				}
				continue
			}
			g.set(x, row, eighthsUp[lv], role)
			if l.Fill {
				for y := row + 1; y < g.H; y++ {
					g.set(x, y, "█", role)
				}
			}
		}
	}
}
