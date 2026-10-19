package plot

import (
	"math"
	"strconv"
)

// Pie shows shares of a whole as a ring of cells (a donut), or a full
// disc, with a legend at the right: each slice's mark, label and share.
// The disc is drawn in quadrant blocks, four pixels to a cell, starting at
// twelve o'clock and going clockwise.
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

// quadrant glyphs by mask: upper left 1, upper right 2, lower left 4,
// lower right 8.
var quadrants = [16]string{"", "▘", "▝", "▀", "▖", "▌", "▞", "▛", "▗", "▚", "▐", "▜", "▄", "▙", "▟", "█"}

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
	if aspect <= 0 || !finite(aspect) || aspect > 10 {
		aspect = 2.2
	}
	legend := p.legend(shares)
	legendW := 0
	for _, it := range legend {
		legendW = max(legendW, 2+Width(it.Label))
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
	// Units: one cell is 1 wide and aspect tall.
	cx, cy := float64(nc)/2, float64(nr)*aspect/2
	r := math.Min(cx, cy)
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
		for i, e := range ends {
			if a < e && shares[i] > 0 {
				return i
			}
		}
		return len(shares) - 1
	}
	y0 := (g.H - nr) / 2
	for y := 0; y < nr; y++ {
		for x := 0; x < nc; x++ {
			// The four quadrant pixels, sampled at their centres.
			var q [4]int
			for k := range 4 {
				px := float64(x) + 0.25 + 0.5*float64(k%2)
				py := (float64(y) + 0.25 + 0.5*float64(k/2)) * aspect
				q[k] = slice(px, py)
			}
			if m == ASCII {
				best := majority(q, -1)
				if best >= 0 {
					g.set(x, y0+y, asciiMarks[best%len(asciiMarks)], p.role(best))
				}
				continue
			}
			fg := majority(q, -1)
			if fg < 0 {
				continue
			}
			empty := q[0] < 0 || q[1] < 0 || q[2] < 0 || q[3] < 0
			bg := -1
			if !empty {
				bg = majority(q, fg)
			}
			mask := 0
			for k := range 4 {
				// Without an empty pixel, a third slice shows as the fg.
				if q[k] == fg || q[k] >= 0 && q[k] != bg {
					mask |= 1 << k
				}
			}
			if bg >= 0 && mask != 15 {
				g.setBg(x, y0+y, quadrants[mask], p.role(fg), p.role(bg))
			} else {
				g.set(x, y0+y, quadrants[mask], p.role(fg))
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
		g.Text(lx+2, ly+i, Truncate(it.Label, g.W-lx-2, ellipsis(m)), Dim, false)
	}
	return g
}

// majority returns the slice most of q show, other than skip, or -1.
func majority(q [4]int, skip int) int {
	best, bestN := -1, 0
	for _, a := range q {
		if a < 0 || a == skip {
			continue
		}
		n := 0
		for _, b := range q {
			if b == a {
				n++
			}
		}
		if n > bestN || n == bestN && a < best {
			best, bestN = a, n
		}
	}
	return best
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
