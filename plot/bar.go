package plot

import "math"

// The eighth blocks, from empty to full.
var (
	eighthsUp    = [...]string{" ", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	eighthsRight = [...]string{" ", "▏", "▎", "▍", "▌", "▋", "▊", "▉", "█"}
)

// Bar is a bar chart: one bar per value, from zero. Negative values draw as
// zero.
type Bar struct {
	Values []float64
	Labels []string // one per value, or none
	Role   Role     // the bars; Series1 when empty
	Roles  []Role   // per bar, over Role
	// Horizontal draws one bar per row, with the label at the left and the
	// value at the right. Otherwise each value is a column.
	Horizontal bool
	// Max is the value of a full bar. 0 means the largest value.
	Max float64
	// Format writes a value for a horizontal bar. Nil means Compact.
	Format func(float64) string
	// Guide fills the rest of a horizontal bar's row with a dotted line.
	Guide bool
}

func (b Bar) role(i int) Role {
	if i < len(b.Roles) && b.Roles[i] != None {
		return b.Roles[i]
	}
	if b.Role != None {
		return b.Role
	}
	return Series1
}

func (b Bar) top() float64 {
	if b.Max > 0 && finite(b.Max) {
		return b.Max
	}
	_, hi, ok := extent(b.Values)
	if !ok || hi <= 0 {
		return 1
	}
	return hi
}

// Draw draws the chart into a w by h grid.
func (b Bar) Draw(w, h int, m Mode) *Grid {
	g := NewGrid(w, h)
	if len(b.Values) == 0 || g.W == 0 || g.H == 0 {
		return g
	}
	if b.Horizontal {
		b.drawRows(g, m)
	} else {
		b.drawColumns(g, m)
	}
	return g
}

func (b Bar) drawRows(g *Grid, m Mode) {
	n := min(len(b.Values), g.H)
	format := b.Format
	if format == nil {
		format = Compact
	}
	labelW, valueW := 0, 0
	for i := 0; i < n; i++ {
		if i < len(b.Labels) {
			labelW = max(labelW, Width(b.Labels[i]))
		}
		valueW = max(valueW, Width(format(b.Values[i])))
	}
	labelW = min(labelW, g.W/3)
	valueW = min(valueW, g.W/4)
	x0 := 0
	if labelW > 0 {
		x0 = labelW + 1
	}
	cells := g.W - x0
	if valueW > 0 {
		cells -= valueW + 1
	}
	cells = max(cells, 0)
	top := b.top()
	for i := 0; i < n; i++ {
		if i < len(b.Labels) && labelW > 0 {
			g.Text(0, i, Truncate(b.Labels[i], labelW, ellipsis(m)), Dim, false)
		}
		v := b.Values[i]
		units := int(math.Round(unit(math.Max(v, 0), 0, top) * float64(cells*8)))
		if !finite(v) {
			units = 0
		}
		x := x0
		for ; units >= 8; units -= 8 {
			g.set(x, i, block(m, "█", "#"), b.role(i))
			x++
		}
		if units > 0 {
			g.set(x, i, block(m, eighthsRight[units], "#"), b.role(i))
			x++
		}
		if b.Guide && m != ASCII {
			for ; x < x0+cells; x++ {
				g.set(x, i, "·", Edge)
			}
		}
		if valueW > 0 {
			g.TextRight(g.W, i, Truncate(format(v), valueW, ellipsis(m)), Fg, false)
		}
	}
}

func (b Bar) drawColumns(g *Grid, m Mode) {
	vals, labels := b.Values, b.Labels
	rows := g.H
	withLabels := len(labels) > 0 && g.H >= 3
	if withLabels {
		rows--
	}
	// Fit: one cell and a gap per bar when there is room, else no gap, else
	// the most recent bars.
	gap := 1
	if 2*len(vals)-1 > g.W {
		gap = 0
	}
	if len(vals) > g.W {
		drop := len(vals) - g.W
		vals = vals[drop:]
		if len(labels) > drop {
			labels = labels[drop:]
		} else {
			labels = nil
		}
		b.Roles = shift(b.Roles, drop)
	}
	n := len(vals)
	colW := max(1, (g.W+gap)/n-gap)
	if colW > 1 {
		// Wide columns read as a block; keep them at most three cells.
		colW = min(colW, 3)
	}
	top := b.top()
	for i, v := range vals {
		x := i * (colW + gap)
		units := 0
		if finite(v) {
			units = int(math.Round(unit(math.Max(v, 0), 0, top) * float64(rows*8)))
		}
		for r := 0; r < rows; r++ {
			lv := units - r*8
			glyph := ""
			switch {
			case lv >= 8:
				glyph = block(m, "█", "#")
			case lv > 0 && m == ASCII:
				if lv >= 4 {
					glyph = "#"
				}
			case lv > 0:
				glyph = eighthsUp[lv]
			}
			if glyph == "" {
				continue
			}
			for k := 0; k < colW; k++ {
				g.set(x+k, rows-1-r, glyph, b.role(i))
			}
		}
		if withLabels && i < len(labels) {
			g.Text(x, g.H-1, Truncate(labels[i], colW, ""), Mute, false)
		}
	}
}

func shift(r []Role, n int) []Role {
	if n >= len(r) {
		return nil
	}
	return r[n:]
}

// block picks the glyph for the mode: the unicode one, or the ASCII one.
func block(m Mode, uni, ascii string) string {
	if m == ASCII {
		return ascii
	}
	return uni
}

func ellipsis(m Mode) string {
	if m == ASCII {
		return "~"
	}
	return "…"
}

// Sparkline is a one-row trend: one cell per value, the height in eighths.
type Sparkline struct {
	Values []float64
	Role   Role // Series1 when empty
	// Min and Max fix the range. Both 0 means the range of the values.
	Min, Max float64
}

var asciiLevels = [...]string{"_", ".", "-", "~", "=", "+", "*", "#"}

// Draw draws the sparkline into a w by h grid, on the bottom row. More
// values than cells are averaged into buckets. Fewer values take fewer
// cells, from the left.
func (s Sparkline) Draw(w, h int, m Mode) *Grid {
	g := NewGrid(w, h)
	if g.W == 0 || g.H == 0 || len(s.Values) == 0 {
		return g
	}
	vals := s.Values
	if len(vals) > g.W {
		vals = buckets(vals, g.W)
	}
	lo, hi := s.Min, s.Max
	if lo == 0 && hi == 0 || !finite(lo) || !finite(hi) || hi < lo {
		var ok bool
		if lo, hi, ok = extent(vals); !ok {
			return g
		}
	}
	role := s.Role
	if role == None {
		role = Series1
	}
	for i, v := range vals {
		if !finite(v) {
			continue
		}
		k := int(math.Round(unit(v, lo, hi) * 7))
		if hi-lo <= 0 {
			k = 3
		}
		k = max(0, min(7, k))
		glyph := eighthsUp[k+1]
		if m == ASCII {
			glyph = asciiLevels[k]
		}
		g.set(i, g.H-1, glyph, role)
	}
	return g
}

// GaugeStyle is how a gauge draws its track.
type GaugeStyle uint8

// The gauge styles.
const (
	GaugeLine   GaugeStyle = iota // a heavy line: ━━━━━━━━━━
	GaugeBlocks                   // eighth blocks on a shaded track: ████▌░░░
)

// Gauge is a value against a maximum, on one row, with the percentage or a
// label at the right.
type Gauge struct {
	Value float64
	Max   float64 // 0 means 1
	Role  Role    // the filled part; Series1 when empty
	// Label replaces the percentage at the right. "-" shows nothing.
	Label string
	Style GaugeStyle
}

// Fraction returns Value / Max, clamped to [0, 1]. A missing value is 0.
func (gg Gauge) Fraction() float64 {
	top := gg.Max
	if top <= 0 || !finite(top) {
		top = 1
	}
	return unit(gg.Value, 0, top)
}

// Draw draws the gauge into a w by h grid, on the middle row.
func (gg Gauge) Draw(w, h int, m Mode) *Grid {
	g := NewGrid(w, h)
	if g.W == 0 || g.H == 0 {
		return g
	}
	y := (g.H - 1) / 2
	f := gg.Fraction()
	label := gg.Label
	switch {
	case label == "-":
		label = ""
	case label != "":
	case !finite(gg.Value):
		label = "-"
	default:
		label = Percent(f)
	}
	label = Truncate(label, max(g.W/2, 0), "")
	cells := g.W
	if label != "" {
		cells -= Width(label) + 1
		g.TextRight(g.W, y, label, Fg, true)
	}
	if cells <= 0 {
		return g
	}
	role := gg.Role
	if role == None {
		role = Series1
	}
	if m == ASCII {
		inner := cells - 2
		if inner < 1 {
			return g
		}
		n := int(math.Round(f * float64(inner)))
		g.set(0, y, "[", Edge)
		for i := 0; i < inner; i++ {
			if i < n {
				g.set(1+i, y, "#", role)
			} else {
				g.set(1+i, y, "-", Track)
			}
		}
		g.set(cells-1, y, "]", Edge)
		return g
	}
	if gg.Style == GaugeLine {
		n := int(math.Round(f * float64(cells)))
		for i := 0; i < cells; i++ {
			if i < n {
				g.set(i, y, "━", role)
			} else {
				g.set(i, y, "━", Track)
			}
		}
		return g
	}
	units := int(math.Round(f * float64(cells*8)))
	for i := 0; i < cells; i++ {
		lv := units - i*8
		switch {
		case lv >= 8:
			g.set(i, y, "█", role)
		case lv > 0:
			g.set(i, y, eighthsRight[lv], role)
		default:
			g.set(i, y, "░", Track)
		}
	}
	return g
}

// Histogram counts values into equal bins and draws the counts as columns.
// With three rows or more, the bottom row shows the range.
type Histogram struct {
	Values []float64
	Bins   int  // 0 means one bin per two cells, at most 20
	Role   Role // Series1 when empty
	Format func(float64) string
}

// Counts returns the bins' counts, and the range the bins cover.
func (hg Histogram) Counts(bins int) (counts []float64, lo, hi float64) {
	lo, hi, ok := extent(hg.Values)
	if !ok || bins <= 0 {
		return nil, 0, 0
	}
	counts = make([]float64, bins)
	span := hi - lo
	for _, v := range hg.Values {
		if !finite(v) {
			continue
		}
		i := 0
		if span > 0 && finite(span) {
			i = int((v - lo) / span * float64(bins))
		}
		counts[max(0, min(bins-1, i))]++
	}
	return counts, lo, hi
}

// Draw draws the histogram into a w by h grid.
func (hg Histogram) Draw(w, h int, m Mode) *Grid {
	g := NewGrid(w, h)
	if g.W == 0 || g.H == 0 {
		return g
	}
	bins := hg.Bins
	if bins <= 0 {
		bins = min(20, max(1, g.W/2))
	}
	bins = min(bins, g.W)
	counts, lo, hi := hg.Counts(bins)
	if counts == nil {
		return g
	}
	rows := g.H
	axis := g.H >= 3
	if axis {
		rows--
	}
	bar := Bar{Values: counts, Role: hg.Role}
	sub := bar.Draw(g.W, rows, m)
	copy(g.Cells, sub.Cells)
	if axis {
		format := hg.Format
		if format == nil {
			format = Compact
		}
		left, right := format(lo), format(hi)
		if Width(left)+Width(right)+1 <= g.W {
			g.Text(0, g.H-1, left, Mute, false)
			g.TextRight(g.W, g.H-1, right, Mute, false)
		}
	}
	return g
}
