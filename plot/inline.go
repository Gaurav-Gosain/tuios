package plot

import "math"

// InlineBar is a bar one row high for a table cell: Value as a share of
// Max, filled from the left to an eighth of a cell. It draws no label, so
// the cell next to it can hold the value.
//
// Give every row of a table the same Max, so the bars compare.
//
// The idea and the look come from iptop by franlol
// (https://github.com/franlol/iptop, MIT).
type InlineBar struct {
	Value float64
	Max   float64 // 0 means 1
	Role  Role    // the filled part; Series1 when empty
	// Track shows the empty part in the Track role: as a lower half block
	// for BarThin, as the background for BarBlocks.
	Track bool
	Style BarStyle
}

// BarStyle is how an inline bar draws.
type BarStyle uint8

// The bar styles.
const (
	// BarThin is a lower half block, exact to half a cell. It is half as
	// tall as the row, so the bars of a table stay apart: ▄▄▄▄▖
	BarThin BarStyle = iota
	// BarBlocks fills the row with eighth blocks, exact to an eighth of a
	// cell: ████▊
	BarBlocks
)

// Fraction returns Value / Max, clamped to [0, 1]. A missing or negative
// value is 0.
func (b InlineBar) Fraction() float64 {
	top := b.Max
	if top <= 0 || !finite(top) {
		top = 1
	}
	return unit(b.Value, 0, top)
}

// Draw draws the bar into a w by h grid, on the middle row.
func (b InlineBar) Draw(w, h int, m Mode) *Grid {
	g := NewGrid(w, h)
	if g.W == 0 || g.H == 0 {
		return g
	}
	role := b.Role
	if role == None {
		role = Series1
	}
	y := (g.H - 1) / 2
	if b.Style == BarThin {
		lineRight(g, y, 0, g.W, b.Fraction(), role, b.Track, m)
	} else {
		fillRight(g, y, 0, g.W, b.Fraction(), role, b.Track, m)
	}
	return g
}

// DivergingBar is a bar one row high that grows left from a centre mark for
// one value and right for another, such as download and upload of one
// service. Each side is a share of Max.
//
// Give every row of a table the same Max, so the bars compare.
//
// The idea and the look come from iptop by franlol
// (https://github.com/franlol/iptop, MIT).
type DivergingBar struct {
	Left, Right float64
	// Max is the value that fills a side. 0 means the larger of Left and
	// Right, which fills that side.
	Max float64
	// LeftRole and RightRole colour the two sides: Series1 and Series2
	// when empty.
	LeftRole, RightRole Role
	// Track shows the empty part, as for InlineBar.
	Track bool
	Style BarStyle
}

// Fractions returns Left / Max and Right / Max, each clamped to [0, 1].
func (d DivergingBar) Fractions() (left, right float64) {
	top := d.Max
	if top <= 0 || !finite(top) {
		top = 0
		for _, v := range []float64{d.Left, d.Right} {
			if finite(v) {
				top = math.Max(top, v)
			}
		}
	}
	if top <= 0 {
		return 0, 0
	}
	return unit(d.Left, 0, top), unit(d.Right, 0, top)
}

// Draw draws the bar into a w by h grid, on the middle row. The centre
// mark takes the middle cell. With an even width the right side is one
// cell wider.
func (d DivergingBar) Draw(w, h int, m Mode) *Grid {
	g := NewGrid(w, h)
	if g.W == 0 || g.H == 0 {
		return g
	}
	y := (g.H - 1) / 2
	lr, rr := d.LeftRole, d.RightRole
	if lr == None {
		lr = Series1
	}
	if rr == None {
		rr = Series2
	}
	left, right := d.Fractions()
	lw := (g.W - 1) / 2
	centre := block(m, "┊", "|")
	if d.Style == BarThin {
		lineLeft(g, y, 0, lw, left, lr, d.Track, m)
		lineRight(g, y, lw+1, g.W-lw-1, right, rr, d.Track, m)
		g.set(lw, y, centre, Dim)
		return g
	}
	fillLeft(g, y, 0, lw, left, lr, d.Track, m)
	fillRight(g, y, lw+1, g.W-lw-1, right, rr, d.Track, m)
	if d.Track {
		g.setBg(lw, y, centre, Dim, Track)
	} else {
		g.set(lw, y, centre, Dim)
	}
	return g
}

// lineRight draws a thin bar on cells x0 to x0+n-1 of row y from the left
// to fraction f, to half a cell, and the track after it.
func lineRight(g *Grid, y, x0, n int, f float64, role Role, track bool, m Mode) {
	lineSide(g, y, n, f, role, track, m, func(i int) int { return x0 + i }, "▖")
}

// lineLeft is lineRight growing from the right.
func lineLeft(g *Grid, y, x0, n int, f float64, role Role, track bool, m Mode) {
	lineSide(g, y, n, f, role, track, m, func(i int) int { return x0 + n - 1 - i }, "▗")
}

// lineSide draws the cells of a line bar, the i-th from its base at x(i).
// half is the glyph of a cell half full: the quadrant on the base's side.
func lineSide(g *Grid, y, n int, f float64, role Role, track bool, m Mode, x func(int) int, half string) {
	if n <= 0 {
		return
	}
	units := cellsOf(f, n*2)
	for i := 0; i < n; i++ {
		lv := units - i*2
		switch {
		case lv >= 2:
			g.set(x(i), y, block(m, "▄", "="), role)
		case lv == 1 && m == ASCII:
			g.set(x(i), y, "=", role)
		case lv == 1:
			g.set(x(i), y, half, role)
		case track:
			g.set(x(i), y, block(m, "▄", "-"), Track)
		}
	}
}

// fillRight fills cells x0 to x0+n-1 of row y from the left to fraction f.
func fillRight(g *Grid, y, x0, n int, f float64, role Role, track bool, m Mode) {
	if n <= 0 {
		return
	}
	if m == ASCII {
		fillASCII(g, y, x0, n, cellsOf(f, n), false, role, track)
		return
	}
	units := cellsOf(f, n*8)
	for i := 0; i < n; i++ {
		lv := units - i*8
		switch {
		case lv >= 8:
			g.set(x0+i, y, "█", role)
		case lv > 0 && track:
			g.setBg(x0+i, y, eighthsRight[lv], role, Track)
		case lv > 0:
			g.set(x0+i, y, eighthsRight[lv], role)
		case track:
			g.setBg(x0+i, y, " ", None, Track)
		}
	}
}

// fillLeft fills cells x0 to x0+n-1 of row y from the right to fraction f.
// Block elements anchored at the right come only in an eighth and a half,
// so on a track the partial cell is a left block in the track's colour on
// a ground of the role, which is exact to an eighth. Without a track it is
// the nearest of the two.
func fillLeft(g *Grid, y, x0, n int, f float64, role Role, track bool, m Mode) {
	if n <= 0 {
		return
	}
	if m == ASCII {
		fillASCII(g, y, x0, n, cellsOf(f, n), true, role, track)
		return
	}
	units := cellsOf(f, n*8)
	for i := 0; i < n; i++ {
		x := x0 + n - 1 - i
		lv := units - i*8
		switch {
		case lv >= 8:
			g.set(x, y, "█", role)
		case lv > 0 && track:
			g.setBg(x, y, eighthsRight[8-lv], Track, role)
		case lv > 4:
			g.set(x, y, "█", role)
		case lv > 1:
			g.set(x, y, "▐", role)
		case lv > 0:
			g.set(x, y, "▕", role)
		case track:
			g.setBg(x, y, " ", None, Track)
		}
	}
}

// cellsOf returns f of n units, rounded, and at least 1 when f is above 0,
// so a value above zero always shows.
func cellsOf(f float64, n int) int {
	k := int(math.Round(f * float64(n)))
	if f > 0 && k == 0 {
		k = 1
	}
	return min(k, n)
}

// fillASCII fills k of n cells with #, from the right when fromRight, and
// the rest with . on a track.
func fillASCII(g *Grid, y, x0, n, k int, fromRight bool, role Role, track bool) {
	for i := 0; i < n; i++ {
		x := x0 + i
		if fromRight {
			x = x0 + n - 1 - i
		}
		switch {
		case i < k:
			g.set(x, y, "#", role)
		case track:
			g.set(x, y, ".", Track)
		}
	}
}
