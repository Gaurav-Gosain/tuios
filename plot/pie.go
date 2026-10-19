package plot

import (
	"math"
	"sort"
	"strconv"
)

// Pie shows shares of a whole as a ring of cells (a donut), or a full
// disc, with a legend at the right: each slice's mark, label and share.
// The disc is drawn in half blocks, two square pixels to a cell, each
// coloured by the slice that covers most of it, starting at twelve o'clock
// and going clockwise.
type Pie struct {
	Values []float64 // negative and missing values count as 0
	Labels []string
	Roles  []Role // per slice; SeriesRole(i) when empty
	// Donut leaves a hole in the middle.
	Donut bool
	// Aspect is a cell's height over its width. 0 means 2.2, a common
	// terminal font. The disc is round when it matches the font.
	Aspect float64
}

// minArc is the smallest share of the circle a slice takes on screen, so
// a slice of 1% is still a visible arc. The legend keeps the real share.
const minArc = 0.035

// maxSlices bounds the slices drawn: the slices after the first
// maxSlices-1 fold into one, labelled "other".
const maxSlices = 12

// fold returns the pie with its slices after the first maxSlices-1 summed
// into one.
func (p Pie) fold() Pie {
	if len(p.Values) <= maxSlices {
		return p
	}
	q := p
	q.Values = append([]float64(nil), p.Values[:maxSlices-1]...)
	rest := 0.0
	for _, v := range p.Values[maxSlices-1:] {
		if finite(v) && v > 0 {
			rest += v
		}
	}
	q.Values = append(q.Values, rest)
	q.Labels = append(append([]string(nil), p.Labels[:min(len(p.Labels), maxSlices-1)]...), make([]string, max(0, maxSlices-1-len(p.Labels)))...)
	q.Labels = append(q.Labels, "other")
	if len(p.Roles) > maxSlices-1 {
		q.Roles = p.Roles[:maxSlices-1]
	}
	return q
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

// arcs returns where each slice ends on the circle, from 0 to 1: the
// shares, with every slice that has a share widened to at least minArc
// and the rest scaled down to make room.
func arcs(shares []float64) []float64 {
	n := 0
	for _, s := range shares {
		if s > 0 {
			n++
		}
	}
	draw := make([]float64, len(shares))
	small, big := 0.0, 0.0
	for i, s := range shares {
		switch {
		case s <= 0:
		case s < minArc && float64(n)*minArc < 1:
			draw[i] = minArc
			small += minArc
		default:
			draw[i] = s
			big += s
		}
	}
	if big > 0 {
		k := (1 - small) / big
		for i, s := range shares {
			if draw[i] == s && s > 0 {
				draw[i] = s * k
			}
		}
	}
	ends := make([]float64, len(draw))
	acc := 0.0
	for i, d := range draw {
		acc += d
		ends[i] = acc
	}
	return ends
}

// Draw draws the pie into a w by h grid: the disc at the left, the legend
// at the right when there is room.
func (p Pie) Draw(w, h int, m Mode) *Grid {
	p = p.fold()
	g := NewGrid(w, h)
	if g.W == 0 || g.H == 0 {
		return g
	}
	shares, ok := p.shares()
	if !ok {
		return g
	}
	aspect := p.Aspect
	if aspect <= 0 || !finite(aspect) || aspect > 10 {
		aspect = 2.2
	}
	legend := p.legend(shares)
	legendW := 0
	for _, it := range legend {
		legendW = max(legendW, 2+width(it.Label))
	}
	// The disc's size in cells: as tall as the grid and as wide as that is
	// round, leaving room for the legend.
	room := g.W
	if legendW > 0 && g.W >= legendW+8 {
		room = g.W - legendW - 2
	}
	rows := float64(g.H)
	cols := rows * aspect
	if cols > float64(room) {
		cols = float64(room)
		rows = cols / aspect
	}
	nc, nr := int(math.Floor(cols)), int(math.Ceil(rows))
	if nc < 1 || nr < 1 {
		return g
	}
	// Units: one cell is 1 wide and aspect tall. Each cell holds two
	// pixels, its upper and lower half, so a pixel is 1 wide and aspect/2
	// tall: square at the common 1:2 cell. The disc is centred on the
	// pixel grid.
	ph := aspect / 2
	cx, cy := float64(nc)/2, float64(2*nr)*ph/2
	r := math.Min(cx, cy) - 0.02
	hole := 0.0
	if p.Donut {
		hole = 0.56
	}
	ends := arcs(shares)
	slice := func(x, y float64) int {
		dx, dy := x-cx, y-cy
		d := math.Hypot(dx, dy) / r
		if d > 1 || d < hole {
			return -1
		}
		// Clockwise from twelve o'clock.
		a := math.Atan2(dx, -dy) / (2 * math.Pi)
		if a < 0 {
			a++
		}
		i := sort.SearchFloat64s(ends, a)
		for i < len(ends) && (shares[i] <= 0 || ends[i] <= a) {
			i++
		}
		return min(i, len(shares)-1)
	}
	// pixel returns the slice that covers most of a pixel, from a 4 by 4
	// sample grid inside it, or -1 when less than half of it is on the
	// ring.
	const ss = 4
	count := make([]int, len(shares))
	pixel := func(px, py int) int {
		for i := range count {
			count[i] = 0
		}
		on := 0
		for sy := 0; sy < ss; sy++ {
			for sx := 0; sx < ss; sx++ {
				x := float64(px) + (float64(sx)+0.5)/ss
				y := (float64(py) + (float64(sy)+0.5)/ss) * ph
				if k := slice(x, y); k >= 0 {
					count[k]++
					on++
				}
			}
		}
		if on*2 < ss*ss {
			return -1
		}
		best := -1
		for k, c := range count {
			if best < 0 || c > count[best] {
				best = k
			}
		}
		return best
	}
	y0 := (g.H - nr) / 2
	for y := 0; y < nr; y++ {
		for x := 0; x < nc; x++ {
			up, down := pixel(x, 2*y), pixel(x, 2*y+1)
			if m == ASCII {
				k := up
				if k < 0 {
					k = down
				}
				if k >= 0 {
					g.set(x, y0+y, asciiMarks[k%len(asciiMarks)], p.role(k))
				}
				continue
			}
			switch {
			case up >= 0 && up == down:
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
	if legendW == 0 || nc+2+legendW > g.W {
		return g
	}
	lx := nc + 2
	ly := max(0, (g.H-len(legend))/2)
	for i, it := range legend {
		if ly+i >= g.H {
			break
		}
		mark := block(m, "■", asciiMarks[i%len(asciiMarks)])
		g.set(lx, ly+i, mark, it.Role)
		g.Text(lx+2, ly+i, truncate(it.Label, g.W-lx-2, ellipsis(m)), Dim, false)
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
	p = p.fold()
	shares, ok := p.shares()
	if !ok {
		return nil
	}
	return p.legend(shares)
}
