package plot

import "math"

// The eighth blocks, from empty to full.
var (
	eighthsUp    = [...]string{" ", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	eighthsRight = [...]string{" ", "▏", "▎", "▍", "▌", "▋", "▊", "▉", "█"}
)

// Bar is a bar chart: one bar per value. Negative values draw as the
// base.
type Bar struct {
	Values []float64
	Labels []string // one per value, or none
	Role   Role     // the bars; Series1 when empty
	Roles  []Role   // per bar, over Role
	// Horizontal draws one bar per row: the label at the left, the value
	// at the right. Otherwise each value is a column.
	Horizontal bool
	// Min and Max fix the range. Both 0 means automatic: from zero, or, for
	// columns whose values all sit far above zero, from a round base under
	// the smallest value. The axis labels the base, so the chart says
	// where it starts.
	Min, Max float64
	// Format writes a value: the value column of horizontal bars and the
	// tick labels of columns. Nil means Compact.
	Format func(float64) string
	// Guide fills the rest of a horizontal bar's row with a dotted line.
	Guide bool
	// Axis draws tick labels at the left of columns, a base line, and the
	// first, middle and last label under it.
	Axis bool
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

func (b Bar) format() func(float64) string {
	if b.Format != nil {
		return b.Format
	}
	return Compact
}

// rng returns the range of the bars.
func (b Bar) rng() (lo, hi float64) {
	if (b.Min != 0 || b.Max != 0) && finite(b.Min) && finite(b.Max) && b.Max > b.Min {
		return b.Min, b.Max
	}
	vlo, vhi, ok := extent(b.Values)
	if !ok || vhi <= 0 {
		return 0, 1
	}
	if !b.Horizontal && vlo > 0 && vlo > vhi/2 {
		// Columns that cluster far from zero: start from a round base
		// under the smallest value, so the differences show.
		lo, hi := niceRange(vlo-(vhi-vlo)*0.3, vhi, 4)
		if lo < 0 {
			lo = 0
		}
		return lo, hi
	}
	if b.Horizontal {
		return 0, vhi
	}
	_, hi = niceRange(0, vhi, 4)
	return 0, hi
}

// Draw draws the chart into a w by h grid.
func (b Bar) Draw(w, h int, m Mode) *Grid {
	g := NewGrid(w, h)
	if len(b.Values) == 0 || g.W == 0 || g.H == 0 {
		return g
	}
	if b.Horizontal {
		b.drawRows(g, m)
		return g
	}
	lo, hi := b.rng()
	if b.Axis {
		labels := xLabels(b.Labels, len(b.Values))
		// The axis is as wide as the columns, so the first and last labels
		// sit under the first and last column.
		labelW := 0
		for _, t := range []float64{lo, hi, (lo + hi) / 2} {
			labelW = max(labelW, width(b.format()(t)))
		}
		pw := g.W - min(labelW, g.W/4) - 1
		if n := len(b.Values); n > 0 && pw > n {
			step := pw / n
			pw = n*step - max(1, step/4) + 1
		}
		sub := NewGrid(g.W-max(g.W-min(labelW, g.W/4)-1-pw, 0), g.H)
		a := drawAxes(sub, lo, hi, b.format(), labels, m)
		p := a.sub()
		b.drawColumns(p, m, lo, hi, false)
		a.paste(sub, p)
		for y := 0; y < sub.H; y++ {
			copy(g.Cells[y*g.W:y*g.W+sub.W], sub.Cells[y*sub.W:(y+1)*sub.W])
		}
		return g
	}
	b.drawColumns(g, m, lo, hi, true)
	return g
}

func (b Bar) drawRows(g *Grid, m Mode) {
	n := min(len(b.Values), g.H)
	format := b.format()
	labelW, valueW := 0, 0
	for i := 0; i < n; i++ {
		if i < len(b.Labels) {
			labelW = max(labelW, width(b.Labels[i]))
		}
		valueW = max(valueW, width(format(b.Values[i])))
	}
	// The values never clip: the labels give way first, then the bars.
	valueW = min(valueW, max(g.W-4, 0))
	labelW = min(labelW, max((g.W-valueW-2)*2/5, 0))
	x0 := 0
	if labelW > 0 {
		x0 = labelW + 1
	}
	cells := g.W - x0
	if valueW > 0 {
		cells -= valueW + 1
	}
	cells = max(cells, 0)
	base, top := b.rng()
	for i := 0; i < n; i++ {
		if i < len(b.Labels) && labelW > 0 {
			g.Text(0, i, truncate(b.Labels[i], labelW, ellipsis(m)), Dim, false)
		}
		v := b.Values[i]
		units := 0
		if finite(v) {
			units = int(math.Round(unit(math.Max(v, base), base, top) * float64(cells*8)))
		}
		x := x0
		for ; units >= 8; units -= 8 {
			g.set(x, i, block(m, "█", "#"), b.role(i))
			x++
		}
		if units > 0 {
			if m == ASCII {
				if units >= 4 {
					g.set(x, i, "#", b.role(i))
				}
			} else {
				g.set(x, i, eighthsRight[units], b.role(i))
			}
			x++
		}
		if b.Guide && m != ASCII {
			for ; x < x0+cells; x++ {
				if (x-x0)%2 == 1 {
					g.set(x, i, "·", Edge)
				}
			}
		}
		if valueW > 0 {
			g.TextRight(g.W, i, truncate(format(v), valueW, ellipsis(m)), Fg, false)
		}
	}
}

// drawColumns draws one column per value between lo and hi. With labels,
// and three rows or more, the bottom row names each column that has room.
func (b Bar) drawColumns(g *Grid, m Mode, lo, hi float64, withLabels bool) {
	vals, labels, roles := b.Values, b.Labels, b.Roles
	rows := g.H
	withLabels = withLabels && len(labels) > 0 && g.H >= 3
	if withLabels {
		rows--
	}
	if rows <= 0 || g.W <= 0 {
		return
	}
	// More values than cells: the most recent ones.
	if len(vals) > g.W {
		drop := len(vals) - g.W
		vals = vals[drop:]
		labels = shiftS(labels, drop)
		roles = shift(roles, drop)
	}
	n := len(vals)
	// Every column has the same width and gap, so the spacing is even.
	step := max(1, g.W/n)
	gap := 0
	if step > 1 {
		gap = max(1, step/4)
	}
	colW := min(max(1, step-gap), 8)
	// In ASCII, columns of different roles take the legend's marks: the
	// first role seen is *, the next +, and so on, as DrawLegend numbers
	// them.
	markOf := map[Role]string{}
	for _, r := range roles {
		if _, ok := markOf[r]; !ok && r != None {
			markOf[r] = asciiMarks[len(markOf)%len(asciiMarks)]
		}
	}
	for i, v := range vals {
		x := i * step
		if x >= g.W {
			break
		}
		units := 0
		if finite(v) {
			units = int(math.Round(unit(v, lo, hi) * float64(rows*8)))
			if v > lo && units == 0 {
				units = 1 // a value above the base always shows
			}
		}
		role := b.Role
		if i < len(roles) && roles[i] != None {
			role = roles[i]
		}
		if role == None {
			role = Series1
		}
		full := block(m, "█", "#")
		if mk, ok := markOf[role]; ok && m == ASCII {
			full = mk
		}
		for r := 0; r < rows; r++ {
			lv := units - r*8
			glyph := ""
			switch {
			case lv >= 8:
				glyph = full
			case lv > 0 && m == ASCII:
				if lv >= 4 {
					glyph = full
				} else if r == 0 {
					glyph = "_"
				}
			case lv > 0:
				glyph = eighthsUp[lv]
			}
			if glyph == "" {
				continue
			}
			for k := 0; k < colW && x+k < g.W; k++ {
				g.set(x+k, rows-1-r, glyph, role)
			}
		}
		if withLabels && i < len(labels) {
			g.Text(x, g.H-1, truncate(labels[i], step, ""), Mute, false)
		}
	}
}

func shift(r []Role, n int) []Role {
	if n >= len(r) {
		return nil
	}
	return r[n:]
}

func shiftS(r []string, n int) []string {
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

// Draw draws the sparkline into a w by h grid. On one row each value is an
// eighth block. On more rows it is a column chart with no axis. More
// values than cells are averaged into buckets; fewer take fewer cells,
// from the left.
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
	levels := g.H * 8
	for i, v := range vals {
		if !finite(v) {
			continue
		}
		// The lowest value keeps one eighth, so the line never vanishes.
		k := 1 + int(math.Round(unit(v, lo, hi)*float64(levels-1)))
		if hi-lo <= 0 {
			k = levels / 2
		}
		if m == ASCII && g.H == 1 {
			g.set(i, 0, asciiLevels[max(0, min(7, k-1))], role)
			continue
		}
		for r := 0; r < g.H; r++ {
			lv := k - r*8
			switch {
			case lv >= 8:
				g.set(i, g.H-1-r, block(m, "█", "#"), role)
			case lv > 0:
				if m == ASCII {
					g.set(i, g.H-1-r, asciiLevels[lv-1], role)
				} else {
					g.set(i, g.H-1-r, eighthsUp[lv], role)
				}
			}
		}
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
	label = truncate(label, max(g.W/2, 0), "")
	cells := g.W
	if label != "" {
		cells -= width(label) + 1
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
			g.setBg(i, y, eighthsRight[lv], role, Track)
		default:
			g.set(i, y, "░", Track)
		}
	}
	return g
}

// maxBins bounds a histogram's bins.
const maxBins = 1000

// Histogram counts values into equal bins and draws the counts as columns,
// with the counts at the left and the range of each end under them.
type Histogram struct {
	Values []float64
	Bins   int  // 0 means one bin per two cells, at most 20
	Role   Role // Series1 when empty
	// Format writes the values at the ends of the range. Nil means Compact.
	Format func(float64) string
}

// Counts returns the bins' counts, and the range the bins cover.
func (hg Histogram) Counts(bins int) (counts []float64, lo, hi float64) {
	lo, hi, ok := extent(hg.Values)
	bins = min(bins, maxBins)
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
	// More bins than cells cannot show, and the count sizes an allocation.
	bins = min(bins, max(g.W, 1), maxBins)
	counts, lo, hi := hg.Counts(bins)
	if counts == nil {
		return g
	}
	format := hg.Format
	if format == nil {
		format = Compact
	}
	_, top, _ := extent(counts)
	a := drawAxes(g, 0, top, func(v float64) string { return Compact(math.Round(v)) },
		[3]string{format(lo), "", format(hi)}, m)
	p := a.sub()
	// Columns touch, as a histogram's bins do, and fill the width.
	colW := max(1, p.W/len(counts))
	for i, c := range counts {
		units := int(math.Round(unit(c, 0, top) * float64(p.H*8)))
		if c > 0 && units == 0 {
			units = 1
		}
		for r := 0; r < p.H; r++ {
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
			role := hg.Role
			if role == None {
				role = Series1
			}
			for k := 0; k < colW; k++ {
				// Wide bins keep a one-cell gap, so they read apart.
				if colW > 2 && k == colW-1 {
					continue
				}
				p.set(i*colW+k, p.H-1-r, glyph, role)
			}
		}
	}
	a.paste(g, p)
	return g
}
