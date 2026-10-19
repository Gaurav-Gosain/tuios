package plot

import (
	"math"
	"strconv"
)

// Pie shows shares of a whole as a ring of cells (a donut), or a full
// disc, with a legend at the right: each slice's mark, label and share.
type Pie struct {
	Values []float64 // negative and missing values count as 0
	Labels []string
	Roles  []Role // per slice; SeriesRole(i) when empty
	// Donut leaves a hole in the middle.
	Donut bool
	// Aspect is a cell's height over its width. 0 means 2.2, a common
	// terminal font. The disc stays round when it matches the font.
	Aspect float64
}

func (p Pie) role(i int) Role {
	if i < len(p.Roles) && p.Roles[i] != None {
		return p.Roles[i]
	}
	return SeriesRole(i)
}

// shares returns each slice's share of the total, and false when the total
// is not positive.
func (p Pie) shares() ([]float64, bool) {
	total := 0.0
	for _, v := range p.Values {
		if finite(v) && v > 0 {
			total += v
		}
	}
	if total <= 0 || !finite(total) {
		return nil, false
	}
	out := make([]float64, len(p.Values))
	for i, v := range p.Values {
		if finite(v) && v > 0 {
			out[i] = v / total
		}
	}
	return out, true
}

// Draw draws the pie into a w by h grid: the disc at the left, the legend
// at the right when there is room.
func (p Pie) Draw(w, h int, m Mode) *Grid {
	g := NewGrid(w, h)
	if g.W == 0 || g.H == 0 {
		return g
	}
	shares, ok := p.shares()
	if !ok {
		return g
	}
	aspect := p.Aspect
	if aspect <= 0 || !finite(aspect) {
		aspect = 2.2
	}
	legend := p.legend(shares)
	legendW := 0
	for _, it := range legend {
		legendW = max(legendW, 2+Width(it.Label))
	}
	// The disc's size in cells: as tall as the grid, as wide as that is
	// round, and never so wide that the legend has no room.
	room := g.W
	if legendW > 0 && g.W >= legendW+8 {
		room = g.W - legendW - 2
	}
	diaRows := float64(g.H)
	diaCols := diaRows * aspect
	if diaCols > float64(room) {
		diaCols = float64(room)
		diaRows = diaCols / aspect
	}
	cols, rows := int(math.Round(diaCols)), int(math.Ceil(diaRows))
	if cols < 1 || rows < 1 {
		return g
	}
	// Each cell holds two pixels, its upper and lower half. A pixel is
	// aspect/2 times as tall as it is wide.
	r := float64(cols) / 2
	cx, cy := r, float64(rows) // in pixel units: x in cells, y in half cells
	py := aspect / 2
	hole := 0.0
	if p.Donut {
		hole = 0.55
	}
	slice := func(x, y float64) int {
		dx, dy := x-cx, (y-cy)*py
		d := math.Hypot(dx, dy) / r
		if d > 1 || d < hole {
			return -1
		}
		// Clockwise from twelve o'clock.
		a := math.Atan2(dx, -dy) / (2 * math.Pi)
		if a < 0 {
			a++
		}
		acc := 0.0
		for i, s := range shares {
			acc += s
			if a < acc {
				return i
			}
		}
		return len(shares) - 1
	}
	y0 := (g.H - rows) / 2
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			up := slice(float64(x)+0.5, float64(2*y)+0.5)
			down := slice(float64(x)+0.5, float64(2*y)+1.5)
			if m == ASCII {
				i := up
				if i < 0 {
					i = down
				}
				if i >= 0 {
					g.set(x, y0+y, asciiMarks[i%len(asciiMarks)], p.role(i))
				}
				continue
			}
			switch {
			case up >= 0 && down >= 0 && up == down:
				g.set(x, y0+y, "█", p.role(up))
			case up >= 0 && down >= 0:
				g.setBg(x, y0+y, "▀", p.role(up), p.role(down))
			case up >= 0:
				g.set(x, y0+y, "▀", p.role(up))
			case down >= 0:
				g.set(x, y0+y, "▄", p.role(down))
			}
		}
	}
	if legendW == 0 || cols+2+legendW > g.W {
		return g
	}
	lx := cols + 2
	ly := max(0, (g.H-len(legend))/2)
	for i, it := range legend {
		if ly+i >= g.H {
			break
		}
		mark := block(m, "■", asciiMarks[i%len(asciiMarks)])
		g.set(lx, ly+i, mark, it.Role)
		g.Text(lx+2, ly+i, Truncate(it.Label, g.W-lx-2, ellipsis(m)), Dim, false)
	}
	return g
}

// legend is each slice's label and share, such as "go 62%".
func (p Pie) legend(shares []float64) []LegendItem {
	var out []LegendItem
	for i, s := range shares {
		label := ""
		if i < len(p.Labels) {
			label = p.Labels[i] + " "
		}
		pct := strconv.Itoa(int(math.Round(s*100))) + "%"
		out = append(out, LegendItem{Label: label + pct, Role: p.role(i)})
	}
	return out
}

// Legend returns the slices' labels with their shares, and their roles.
func (p Pie) Legend() []LegendItem {
	shares, ok := p.shares()
	if !ok {
		return nil
	}
	return p.legend(shares)
}
