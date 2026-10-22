package plot

import (
	"math"
	"slices"
)

// Radar is a radar chart: a spoke for each axis, from twelve o'clock
// clockwise, rings at even steps from the centre to the rim, and each
// series as a closed polygon through its values. The axis labels sit
// outside the rim. In Braille mode the lines are braille dots; in Blocks
// and ASCII modes they are a mark a cell.
//
// A cell shows one colour: where series cross in a cell, the last series
// gives the colour. The host draws the legend from Legend.
type Radar struct {
	Axes   []string
	Series []Series // a value for each axis; missing values sit at the centre
	// Min and Max are the values at the centre and at the rim. Both 0
	// means from 0 to the largest value.
	Min, Max float64
	// Rings is the count of rings; 0 means 4.
	Rings int
	// Polygon draws the rings as polygons through the spokes, not circles.
	Polygon bool
	// Aspect is a cell's height over its width. 0 means 2.2, a common
	// terminal font. The chart is round when it matches the font.
	Aspect float64
}

// MaxRadarAxes is the most axes a radar draws; more are cut.
const MaxRadarAxes = 64

func (r Radar) rng() (lo, hi float64) {
	if (r.Min != 0 || r.Max != 0) && finite(r.Min) && finite(r.Max) && r.Max > r.Min {
		return r.Min, r.Max
	}
	all := make([][]float64, len(r.Series))
	for i, s := range r.Series {
		all[i] = s.Values
	}
	_, vhi, ok := extent(all...)
	if !ok || vhi <= 0 {
		return 0, 1
	}
	return 0, vhi
}

// Legend returns the series names and roles.
func (r Radar) Legend() []LegendItem {
	var out []LegendItem
	for i, s := range r.Series {
		role := s.Role
		if role == None {
			role = SeriesRole(i)
		}
		out = append(out, LegendItem{Label: s.Name, Role: role})
	}
	return out
}

// Draw draws the radar into a w by h grid: the chart as large as fits, a
// row above and below it and a column of label room each side.
func (r Radar) Draw(w, h int, m Mode) *Grid {
	g := NewGrid(w, h)
	n := min(len(r.Axes), MaxRadarAxes)
	if g.W == 0 || g.H < 3 || n < 3 {
		return g
	}
	aspect := r.Aspect
	if aspect <= 0 || !finite(aspect) || aspect > 10 {
		aspect = 2.2
	}
	lo, hi := r.rng()
	rings := r.Rings
	if rings <= 0 {
		rings = 4
	}
	rings = min(rings, 20)
	labels := make([]string, n)
	labelW := 0
	for i := range n {
		labels[i] = truncate(r.Axes[i], 20, ellipsis(m))
		labelW = max(labelW, width(labels[i]))
	}
	// The radius in cell widths: from the rows there are, a row each for
	// the labels above and below, and from the columns there are, a label
	// and two cells each side.
	rows := g.H - 2
	rho := (float64(rows)/2 - 0.25) * aspect
	rho = math.Min(rho, float64(g.W-2*labelW-4)/2-0.5)
	if rho < 2 {
		return g
	}
	// The centre, in cells.
	cx, cy := float64(g.W)/2, 1+float64(rows)/2
	angle := func(i int) float64 { return -math.Pi/2 + 2*math.Pi*float64(i)/float64(n) }
	frac := func(v float64) float64 {
		if !finite(v) || !(hi > lo) {
			return 0
		}
		return (math.Min(math.Max(v, lo), hi) - lo) / (hi - lo)
	}
	// The dots: two across and four down in a cell, in layers. A layer is
	// drawn in one role, and the last layer with dots in a cell colours it.
	type layer struct {
		role Role
		dots []uint8
	}
	var layers []layer
	add := func(role Role) int {
		layers = append(layers, layer{role, make([]uint8, g.W*g.H)})
		return len(layers) - 1
	}
	dw, dh := 2, 4
	if m != Braille {
		dw, dh = 1, 1
	}
	set := func(l, x, y int) {
		if x < 0 || y < 0 || x >= g.W*dw || y >= g.H*dh {
			return
		}
		bit := uint8(1)
		if m == Braille {
			bit = brailleBit(x%2, y%4)
		}
		layers[l].dots[(y/dh)*g.W+x/dw] |= bit
	}
	at := func(a, f float64) (int, int) {
		x := cx + math.Cos(a)*f*rho
		y := cy + math.Sin(a)*f*rho/aspect
		return int(math.Floor(x * float64(dw))), int(math.Floor(y * float64(dh)))
	}
	stroke := func(l, x0, y0, x1, y1, every int) {
		k := 0
		line(x0, y0, x1, y1, func(x, y int) {
			if every <= 1 || k%every == 0 {
				set(l, x, y)
			}
			k++
		})
	}
	grid := add(Mute)
	for t := 1; t <= rings; t++ {
		f := float64(t) / float64(rings)
		if r.Polygon {
			for i := range n {
				x0, y0 := at(angle(i), f)
				x1, y1 := at(angle(i+1), f)
				stroke(grid, x0, y0, x1, y1, 2)
			}
			continue
		}
		steps := int(f*rho*12) + 12
		for k := 0; k < steps; k += 2 {
			x, y := at(2*math.Pi*float64(k)/float64(steps), f)
			set(grid, x, y)
		}
	}
	ox, oy := at(0, 0)
	for i := range n {
		x, y := at(angle(i), 1)
		stroke(grid, ox, oy, x, y, 2)
	}
	for k, s := range r.Series {
		role := s.Role
		if role == None {
			role = SeriesRole(k)
		}
		l := add(role)
		val := func(i int) float64 {
			if i < len(s.Values) {
				return frac(s.Values[i])
			}
			return 0
		}
		for i := range n {
			x0, y0 := at(angle(i), val(i))
			x1, y1 := at(angle(i+1), val((i+1)%n))
			stroke(l, x0, y0, x1, y1, 1)
			set(l, x0+1, y0)
		}
	}
	// The labels, a cell past the rim, before the dots: the dots keep out
	// of them.
	for i, s := range labels {
		a := angle(i)
		ux, uy := math.Cos(a), math.Sin(a)
		ax := cx + ux*(rho+1.5)
		ay := cy + uy*(rho+0.6)/aspect
		y := min(max(int(math.Floor(ay)), 0), g.H-1)
		var x int
		switch {
		case math.Abs(ux) < 0.25:
			x = int(math.Round(ax - float64(width(s))/2))
		case ux > 0:
			x = int(math.Round(ax))
		default:
			x = int(math.Round(ax)) - width(s)
		}
		x = min(max(x, 0), g.W-width(s))
		g.Text(x, y, s, Fg, false)
	}
	for y := 0; y < g.H; y++ {
		for x := 0; x < g.W; x++ {
			if c := g.At(x, y); c.Text != "" {
				continue
			}
			for l, layer := range slices.Backward(layers) {
				bits := layer.dots[y*g.W+x]
				if bits == 0 {
					continue
				}
				switch {
				case m == Braille:
					g.set(x, y, string(rune(0x2800+int(bits))), layer.role)
				case l == grid:
					g.set(x, y, block(m, "·", "."), layer.role)
				default:
					g.set(x, y, block(m, "•", asciiMarks[(l-1)%len(asciiMarks)]), layer.role)
				}
				break
			}
		}
	}
	return g
}
