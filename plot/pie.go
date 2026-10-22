package plot

import (
	"math"
	"slices"
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
	// NoLegend draws the disc alone, as large as the grid lets it be. A
	// host that draws its own legend sets it, and takes each slice's mark
	// from Mark.
	NoLegend bool
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

// role is the colour of slice i. Slices take the series colours in turn.
// When the last slice with a share would take the colour of the first, the
// two would meet at twelve o'clock in one colour, so the last takes the
// next colour.
func (p Pie) role(i int) Role {
	if i < len(p.Roles) && p.Roles[i] != None {
		return p.Roles[i]
	}
	if i > 0 && i%len(series) == 0 && i == p.lastShare() {
		return SeriesRole(i + 1)
	}
	return SeriesRole(i)
}

// lastShare is the index of the last slice with a positive value, or -1.
func (p Pie) lastShare() int {
	for i, v := range slices.Backward(p.Values) {
		if finite(v) && v > 0 {
			return i
		}
	}
	return -1
}

// shaded reports whether slice i is drawn in a shade instead of solid. The
// series colours run out after six, and at 16 colours there are no more
// hues to take, so the slices of the second round of colours are shaded.
func (p Pie) shaded(i int) bool {
	return i >= len(series) && (i >= len(p.Roles) || p.Roles[i] == None)
}

// fill is the glyph of a cell that slice i covers whole.
func (p Pie) fill(i int) string {
	if p.shaded(i) {
		return "▒"
	}
	return "█"
}

// Mark returns the legend mark of slice i, after the slices past the
// twelfth are folded into one, and its role: the glyph and colour that
// tell the slice apart in the disc.
func (p Pie) Mark(i int, m Mode) (string, Role) {
	p = p.fold()
	if m == ASCII {
		return asciiMarks[i%len(asciiMarks)], p.role(i)
	}
	return p.fill(i), p.role(i)
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
	labelW, pctW := 0, 0
	for i := range legend {
		label, pct := p.legendParts(shares, i)
		labelW = max(labelW, width(label))
		pctW = max(pctW, width(pct))
	}
	legendW := 0
	if len(legend) > 0 {
		legendW = 2 + pctW
		if labelW > 0 {
			legendW += labelW + 1
		}
	}
	// The disc's size in cells: as tall as the grid and as wide as that is
	// round, leaving room for the legend.
	room := g.W
	if p.NoLegend {
		legendW = 0
	}
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
		for sy := range ss {
			for sx := range ss {
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
	for y := range nr {
		for x := range nc {
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
			// A shade shows in a whole cell only: in a half block the
			// slice would show solid, in the colour of the slice six
			// before it. The cell goes to the shaded slice whole.
			if up >= 0 && down != up && p.shaded(up) {
				down = up
			} else if down >= 0 && up != down && p.shaded(down) {
				up = down
			}
			switch {
			case up >= 0 && up == down:
				g.set(x, y0+y, p.fill(up), p.role(up))
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
	// The shares stand in one column, right-aligned after the longest label.
	labelW = min(labelW, max(0, g.W-lx-2-1-pctW))
	for i := range legend {
		if ly+i >= g.H {
			break
		}
		mark, role := p.Mark(i, m)
		g.set(lx, ly+i, mark, role)
		label, pct := p.legendParts(shares, i)
		x := lx + 2
		if label != "" {
			g.Text(x, ly+i, truncate(label, labelW, ellipsis(m)), Dim, false)
			x += labelW + 1
		}
		g.Text(x+pctW-width(pct), ly+i, pct, Dim, false)
	}
	return g
}

// legendParts is slice i's label and its share, such as "go" and "62%".
func (p Pie) legendParts(shares []float64, i int) (label, pct string) {
	if i < len(p.Labels) {
		label = p.Labels[i]
	}
	return label, strconv.Itoa(int(math.Round(shares[i]*100))) + "%"
}

// legend is each slice's label and share, such as "go 62%".
func (p Pie) legend(shares []float64) []LegendItem {
	var out []LegendItem
	for i := range shares {
		label, pct := p.legendParts(shares, i)
		if label != "" {
			label += " "
		}
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
