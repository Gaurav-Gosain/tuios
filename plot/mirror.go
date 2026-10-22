package plot

import (
	"math"
	"strings"
)

// Mirror is two series around one baseline: Up rises above it and Down
// hangs below it, such as download and upload, tokens in and out, or reads
// and writes. Each value is one column of dots, graded in colour by height,
// and the newest value is at the right edge, as a monitor scrolls. A value
// at or below 0 draws no column.
//
// The idea and the look come from iptop by franlol
// (https://github.com/franlol/iptop, MIT).
type Mirror struct {
	// Up and Down are the two halves. Name is written at the left of the
	// half's corner row, in the series role. Role is Series1 for Up and
	// Series2 for Down when empty.
	Up, Down Series
	// UpRamp and DownRamp grade a half by height, low to high: the row
	// next to the baseline takes the first role and the far row the last.
	// Empty means the series role at fade 2, fade 1 and full.
	UpRamp, DownRamp []Role
	// Shared scales both halves to the larger peak, so their heights
	// compare. Otherwise each half scales to its own peak, which suits two
	// directions a thousand times apart.
	Shared bool
	// Max fixes the top of the scale of both halves. 0 means the peak.
	Max float64
	// Peaks marks the highest value of each half with a dot and the value:
	// above Up and below Down.
	Peaks bool
	// Stats writes "peak · avg · Σ" of the finite values in view at the
	// right of each half's corner row: the top row for Up, the bottom row for Down.
	// The parts that do not fit are left out from the right.
	Stats bool
	// Format writes the values of the peaks and the stats. Nil means
	// Compact.
	Format func(float64) string
}

// half is one side of a mirror, as Draw lays it out.
type half struct {
	vals   []float64 // the values drawn, newest last
	offset int       // the column of the first value, in values
	top    float64
	ramp   []Role
	role   Role
	down   bool
}

func (mr Mirror) format() func(float64) string {
	if mr.Format != nil {
		return mr.Format
	}
	return Compact
}

func (mr Mirror) role(down bool) Role {
	if down {
		if mr.Down.Role != None {
			return mr.Down.Role
		}
		return Series2
	}
	if mr.Up.Role != None {
		return mr.Up.Role
	}
	return Series1
}

func defaultRamp(r Role) []Role { return []Role{Fade(r, 2), Fade(r, 1), r} }

// peakOf returns the largest finite value and its index, or -1.
func peakOf(vals []float64) (float64, int) {
	best, at := 0.0, -1
	for i, v := range vals {
		if finite(v) && (at < 0 || v > best) {
			best, at = v, i
		}
	}
	return best, at
}

// Draw draws the chart into a w by h grid. From the top: Up's corner row,
// Up's peak, Up, Down, Down's peak and Down's corner row. When the grid is
// short, the peak rows go first, then the corner rows. Down needs a row of
// its own: in a grid one row high, only Up draws.
func (mr Mirror) Draw(w, h int, m Mode) *Grid {
	g := NewGrid(w, h)
	if g.W == 0 || g.H == 0 || len(mr.Up.Values) == 0 && len(mr.Down.Values) == 0 {
		return g
	}
	headUp := b2i(mr.Up.Name != "" || mr.Stats)
	headDown := b2i(mr.Down.Name != "" || mr.Stats)
	peaks := b2i(mr.Peaks)
	if g.H-headUp-headDown-2*peaks < 2 {
		peaks = 0
	}
	if g.H-headUp-headDown < 2 {
		headUp, headDown = 0, 0
	}
	rows := g.H - headUp - headDown - 2*peaks
	upRows, downRows := (rows+1)/2, rows/2

	per := 1 // values per cell
	if m == Braille {
		per = 2
	}
	cols := g.W * per
	mk := func(s Series, ramp []Role, down bool) half {
		vals := s.Values
		if len(vals) > cols {
			vals = vals[len(vals)-cols:]
		}
		hf := half{vals: vals, offset: cols - len(vals), ramp: ramp, role: mr.role(down), down: down}
		if len(hf.ramp) == 0 {
			hf.ramp = defaultRamp(hf.role)
		}
		return hf
	}
	up, down := mk(mr.Up, mr.UpRamp, false), mk(mr.Down, mr.DownRamp, true)
	pu, _ := peakOf(up.vals)
	pd, _ := peakOf(down.vals)
	up.top, down.top = pu, pd
	if mr.Shared {
		up.top = math.Max(pu, pd)
		down.top = up.top
	}
	if mr.Max > 0 && finite(mr.Max) {
		up.top, down.top = mr.Max, mr.Max
	}

	y := 0
	if headUp > 0 {
		mr.corner(g, y, mr.Up.Name, up, m)
		y++
	}
	if peaks > 0 {
		mr.peak(g, y, up, per, m)
		y++
	}
	up.draw(g, y, upRows, per, m)
	y += upRows
	down.draw(g, y, downRows, per, m)
	y += downRows
	if peaks > 0 {
		mr.peak(g, y, down, per, m)
		y++
	}
	if headDown > 0 {
		mr.corner(g, y, mr.Down.Name, down, m)
	}
	return g
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// rampRole returns the role of band b of n, counting from the baseline.
func (hf half) rampRole(b, n int) Role {
	k := len(hf.ramp) - 1
	if n > 1 {
		k = int(math.Round(float64(b) * float64(len(hf.ramp)-1) / float64(n-1)))
	}
	return hf.ramp[max(0, min(len(hf.ramp)-1, k))]
}

// level returns the height of value i in units of a full height of n.
func (hf half) level(i, n int) int {
	if i < 0 || i >= len(hf.vals) {
		return 0
	}
	v := hf.vals[i]
	if !finite(v) || v <= 0 || hf.top <= 0 {
		return 0
	}
	// A value above zero always shows.
	return max(1, int(math.Round(math.Min(v, hf.top)/hf.top*float64(n))))
}

// draw draws the half's columns on rows y to y+rows-1. Up grows from the
// bottom row and Down from the top row. Where a column is empty the row on
// the baseline shows a dotted track, so the baseline reads across gaps.
func (hf half) draw(g *Grid, y, rows, per int, m Mode) {
	if rows <= 0 {
		return
	}
	// row returns the grid row of band b, counting from the baseline.
	row := func(b int) int {
		if hf.down {
			return y + b
		}
		return y + rows - 1 - b
	}
	for cx := 0; cx < g.W; cx++ {
		if m == Braille {
			var hs [2]int
			for dx := range hs {
				hs[dx] = hf.level(cx*2+dx-hf.offset, rows*4)
			}
			for b := range rows {
				var bits uint8
				for dx, hgt := range hs {
					for dy := range 4 {
						// d is the dot's distance from the baseline.
						d := b*4 + 3 - dy
						if hf.down {
							d = b*4 + dy
						}
						if d < hgt {
							bits |= brailleBit(dx, dy)
						}
					}
				}
				switch {
				case bits != 0:
					g.set(cx, row(b), string(rune(0x2800+int(bits))), hf.rampRole(b, rows))
				case b == 0 && hf.down:
					g.set(cx, row(b), "⠉", Track)
				case b == 0:
					g.set(cx, row(b), "⣀", Track)
				}
			}
			continue
		}
		units := hf.level(cx-hf.offset, rows*8)
		for b := range rows {
			lv := units - b*8
			glyph := ""
			switch {
			case lv <= 0 && b == 0:
				g.set(cx, row(b), hf.track(m), Track)
				continue
			case lv <= 0:
				continue
			case m == ASCII:
				// A partial cell is ":", so a small value reads apart from
				// the "." of an empty baseline.
				glyph = "#"
				if lv < 4 {
					glyph = ":"
				}
			case lv >= 8:
				glyph = "█"
			case !hf.down:
				glyph = eighthsUp[lv]
			case lv <= 2:
				// Only the upper eighth and the upper half hang from the
				// top of a cell.
				glyph = "▔"
			case lv <= 5:
				glyph = "▀"
			default:
				glyph = "█"
			}
			g.set(cx, row(b), glyph, hf.rampRole(b, rows))
		}
	}
}

func (hf half) track(m Mode) string {
	switch {
	case m == ASCII:
		return "."
	case hf.down:
		return "▔"
	}
	return "▁"
}

// peak writes the half's peak marker on row y: a dot over the peak's
// column and the value beside it, at the right of the dot when it fits.
func (mr Mirror) peak(g *Grid, y int, hf half, per int, m Mode) {
	v, i := peakOf(hf.vals)
	if i < 0 || v <= 0 {
		return
	}
	x := (hf.offset + i) / per
	label := mr.format()(v)
	dot := block(m, "•", "*")
	role := hf.ramp[len(hf.ramp)-1]
	lw := width(label)
	switch {
	case x+2+lw <= g.W:
		g.set(x, y, dot, role)
		g.Text(x+2, y, label, Dim, false)
	case x-1-lw >= 0:
		g.Text(x-1-lw, y, label, Dim, false)
		g.set(x, y, dot, role)
	default:
		g.set(x, y, dot, role)
	}
}

// corner writes the half's name at the left of row y and, with Stats,
// peak, average and total at the right, leaving out from the right the
// parts that do not fit.
func (mr Mirror) corner(g *Grid, y int, name string, hf half, m Mode) {
	name = truncate(name, g.W, ellipsis(m))
	end := g.Text(0, y, name, hf.role, true)
	if !mr.Stats {
		return
	}
	sum, n := 0.0, 0
	for _, v := range hf.vals {
		if finite(v) {
			sum += v
			n++
		}
	}
	if n == 0 {
		return
	}
	peak, _ := peakOf(hf.vals)
	f := mr.format()
	sep, total := " · ", "Σ "
	if m == ASCII {
		sep, total = "  ", "sum "
	}
	parts := []string{"peak " + f(peak), "avg " + f(sum/float64(n)), total + f(sum)}
	room := g.W
	if name != "" {
		room = g.W - end - 2
	}
	for k := len(parts); k > 0; k-- {
		s := strings.Join(parts[:k], sep)
		if width(s) <= room {
			g.TextRight(g.W, y, s, Mute, false)
			return
		}
	}
}
