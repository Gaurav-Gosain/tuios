package plot

import "math"

// Span is one bar across a row from Start to End, on a scale from Min to
// Max across the row: a task on a gantt chart, or any range. It covers
// every cell it reaches into, and at least one cell when it is on the
// scale at all.
type Span struct {
	Start, End float64
	Min, Max   float64
	// Fill is the glyph of the bar, one cell wide; "█" when empty, "#" in
	// ASCII mode.
	Fill string
	Role Role // Series1 when empty
}

// Draw draws the span into a row w cells wide.
func (s Span) Draw(w int, m Mode) *Grid {
	g := NewGrid(w, 1)
	if g.W == 0 || !finite(s.Start) || !finite(s.End) || !finite(s.Min) || !finite(s.Max) {
		return g
	}
	lo, hi := s.Start, s.End
	if hi < lo {
		lo, hi = hi, lo
	}
	if hi < s.Min || lo > s.Max {
		return g
	}
	span := s.Max - s.Min
	at := func(t float64) float64 {
		if span <= 0 {
			return float64(g.W) / 2
		}
		return (t - s.Min) / span * float64(g.W)
	}
	col := func(t float64) int { return min(max(int(math.Floor(at(t))), 0), g.W-1) }
	a := col(lo)
	b := min(max(int(math.Ceil(at(hi)))-1, a), g.W-1)
	fill := s.Fill
	if width(fill) != 1 || m == ASCII && !isASCII(fill) {
		fill = block(m, "█", "#")
	}
	role := s.Role
	if role == None {
		role = Series1
	}
	for x := a; x <= b; x++ {
		g.set(x, 0, fill, role)
	}
	return g
}

func isASCII(s string) bool {
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}
