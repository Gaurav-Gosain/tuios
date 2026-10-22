package text

import (
	"math"

	"tuios.dev/plot"
	"tuios.dev/sketch"
	"tuios.dev/sketch/internal/types"
)

// An XY chart is drawn from its lists with tuios.dev/plot: bars as eighth
// blocks and lines as braille dots, on axes drawn here. Bars and lines share the
// value range of the SVG, and the points of a line sit at the middle of the
// slots the bars stand in. When the columns are too few for a slot per
// category, the chart turns: categories run down as rows, with bars across and
// the values of a line as dots on their rows.

// plotRows is the height of the plot of a chart with columns: from 10 to 16
// rows, the count that puts the ticks nearest to whole rows, so that they are
// evenly spaced.
func plotRows(ticks []types.GeoMark) int {
	best, worst := 12, math.Inf(1)
	for rows := 10; rows <= 16; rows++ {
		off := 0.0
		for _, t := range ticks {
			v := t.At * float64(rows)
			off = math.Max(off, math.Abs(v-math.Round(v)))
		}
		// A taller plot must be clearly better.
		if off < worst-0.05 {
			best, worst = rows, off
		}
	}
	return best
}

func drawXY(l *sketch.Layout, opt Options) (*canvas, error) {
	g := l.XY
	if g == nil {
		return nil, ErrUnsupported
	}
	if !g.Horizontal {
		if c, err := xyColumns(g, opt); err != ErrTooWide {
			return c, err
		}
	}
	return xyAcross(g, opt)
}

// canvasRole maps a role of tuios.dev/plot to a role of the canvas.
func canvasRole(r plot.Role) uint8 {
	for i := range seriesRoles {
		if r == plot.SeriesRole(i) {
			return seriesRole(i)
		}
	}
	switch r {
	case plot.None, plot.Fg:
		return roleText
	}
	return roleLine
}

// strokeOf is the strokes of each light box-drawing glyph.
var strokeOf = func() map[rune]uint8 {
	m := map[rune]uint8{}
	for mask := len(joins) - 1; mask > 0; mask-- {
		if _, ok := m[joins[mask]]; !ok {
			m[joins[mask]] = uint8(mask)
		}
	}
	m['│'], m['─'] = up|down, left|right
	return m
}()

// paste copies a grid of tuios.dev/plot into the canvas from x0, y0: each
// cell's text, its role and its ground.
func (c *canvas) paste(g *plot.Grid, x0, y0 int) {
	for y := 0; y < g.H; y++ {
		for x := 0; x < g.W; x++ {
			cell := g.At(x, y)
			if cell.Text == "" || cell.Text == " " {
				continue
			}
			r := []rune(cell.Text)[0]
			if mask, ok := strokeOf[r]; ok && cell.Bg == plot.None {
				// A line as strokes, so it joins the canvas's own lines and
				// rows of plain vertical lines still close up.
				c.stroke(x0+x, y0+y, mask, light, canvasRole(cell.Role))
				continue
			}
			bg := uint8(roleNone)
			if cell.Bg != plot.None {
				bg = canvasRole(cell.Bg)
			}
			c.putBg(x0+x, y0+y, r, canvasRole(cell.Role), bg)
		}
	}
}

// xyLegend is the legend in lines of at most w cells: a mark in the colour of
// each named series and its name.
type legendItem struct {
	x, y  int
	mark  rune
	role  uint8
	label string
}

func xyLegend(g *types.GeoXY, w int) ([]legendItem, int) {
	var items []legendItem
	x, y := 0, 0
	for i, s := range g.Series {
		if s.Name == "" {
			continue
		}
		mark := '━'
		if s.Bar {
			mark = '█'
		}
		label := fit(s.Name, max(w-2, 1))
		iw := 2 + width(label)
		if x > 0 && x+3+iw > w {
			x, y = 0, y+1
		} else if x > 0 {
			x += 3
		}
		items = append(items, legendItem{x, y, mark, seriesRole(i), label})
		x += iw
	}
	if len(items) == 0 {
		return nil, 0
	}
	return items, y + 1
}

func (c *canvas) legend(items []legendItem, x0, y0 int) {
	for _, it := range items {
		c.put(x0+it.x, y0+it.y, it.mark, it.role)
		c.text(x0+it.x+2, y0+it.y, it.label, roleText)
	}
}

// slotValues are the values of a series by slot, as fractions of the value
// axis, NaN where a slot has none.
func slotValues(g *types.GeoXY, s types.GeoSeries) []float64 {
	vals := make([]float64, g.Slots)
	for i := range vals {
		vals[i] = math.NaN()
	}
	for _, p := range s.Points {
		i := min(max(int(p.At*float64(g.Slots)), 0), g.Slots-1)
		vals[i] = p.Value
	}
	return vals
}

// barParts splits fractions of the value axis at the base into the length of
// each bar past the base and short of it, each as a fraction of that side.
func barParts(vals []float64, base float64) (up, down []float64) {
	up = make([]float64, len(vals))
	down = make([]float64, len(vals))
	for i, v := range vals {
		up[i], down[i] = math.NaN(), math.NaN()
		switch {
		case math.IsNaN(v):
		case v > base && base < 1:
			up[i] = math.Min((v-base)/(1-base), 1)
		case v < base && base > 0:
			down[i] = math.Min((base-v)/base, 1)
		}
	}
	return up, down
}

// barGlyph reports whether r is a block a bar is drawn with.
func barGlyph(r rune) bool {
	return (r >= '▀' && r <= '█') || r == '▔' || r == '▐' || r == '▕' || (r >= '▉' && r <= '▏')
}

func hanging(lv int) rune {
	switch {
	case lv >= 8:
		return '█'
	case lv >= 4:
		return '▀'
	}
	return '▔'
}

func leftward(lv int) rune {
	switch {
	case lv >= 8:
		return '█'
	case lv >= 4:
		return '▐'
	}
	return '▕'
}

func xyColumns(g *types.GeoXY, opt Options) (*canvas, error) {
	if g.Slots <= 0 {
		return nil, ErrUnsupported
	}
	bars := 0
	for _, s := range g.Series {
		if s.Bar {
			bars++
		}
	}
	// The tick labels, right-aligned left of the axis.
	lw := 0
	for _, t := range g.Ticks {
		lw = max(lw, width(t.Label))
	}
	axisX := lw + 1
	if lw == 0 {
		axisX = 0
	}
	px0 := axisX + 1
	// Each slot is wide enough for its bars side by side with a cell to spare,
	// and for its label when there is room.
	least := max(bars, 1) + 1
	if bars > 1 {
		least = 2*bars + 1
	}
	label := 0
	for _, m := range g.Marks {
		label = max(label, width(m.Label))
	}
	// Point labels of a line ask for wider slots too, and the label of the
	// last point for room past the plot.
	point := 0
	for _, s := range g.Series {
		for _, p := range s.Points {
			if !s.Bar {
				point = max(point, min(width(p.Label), 16))
			}
		}
	}
	slotW := min(max(label+2, point+2, least, 4), 12)
	if opt.Cols > 0 {
		avail := opt.Cols - px0 - 1
		slotW = min(max(slotW, 8), avail/g.Slots)
		if slotW < least {
			return nil, ErrTooWide
		}
	}
	pw := g.Slots * slotW
	wd := widen(px0+pw+1, g, opt)
	if point > 0 {
		wd = max(wd, px0+pw+point/2+1)
		if opt.Cols > 0 {
			wd = max(min(wd, opt.Cols), px0+pw+1)
		}
	}

	var title []string
	for _, line := range g.Title {
		title = append(title, wrap(line, wd)...)
	}
	legend, legendH := xyLegend(g, wd)
	y := 0
	if len(title) > 0 {
		y = len(title) + 1
	}
	legendY := y
	if legendH > 0 {
		y += legendH + 1
	}
	valueTitleY := y
	if g.ValueTitle != "" {
		y++
	}
	top := y
	xyRows := plotRows(g.Ticks)
	axisY := top + xyRows
	labelY := axisY + 1
	height := labelY + 1
	var categoryTitle []string
	if g.CategoryTitle != "" {
		categoryTitle = wrap(g.CategoryTitle, wd)
		height += len(categoryTitle)
	}
	c, err := newCanvas(wd, height)
	if err != nil {
		return nil, err
	}
	for i, line := range title {
		c.text((wd-width(line))/2, i, line, roleTitle)
	}
	c.legend(legend, 0, legendY)
	if g.ValueTitle != "" {
		c.text(0, valueTitleY, fit(g.ValueTitle, wd), roleHead)
	}

	// The axes, the ticks and their labels. A tick marks the top edge of its
	// row: the low end of the range is the row of the axis.
	c.vline(axisX, top, axisY, light, roleLine)
	c.hline(axisX, px0+pw-1, axisY, light, roleLine)
	used := map[int]bool{}
	for _, t := range g.Ticks {
		if !(t.At >= 0 && t.At <= 1) {
			continue
		}
		ty := top + xyRows - int(math.Round(t.At*float64(xyRows)))
		if used[ty] {
			continue
		}
		used[ty] = true
		if axisX > 0 {
			c.stroke(axisX, ty, left, light, roleLine)
		}
		c.text(lw-width(t.Label), ty, t.Label, roleText)
	}
	center := func(i int) int { return px0 + i*slotW + slotW/2 }
	at := func(a float64) int { return px0 + min(max(int(a*float64(pw)), 0), pw-1) }
	if g.Band && len(g.Marks) == g.Slots {
		every := 1
		if label > 0 {
			every = max(1, (min(label, 16)+slotW)/slotW)
		}
		for i := 0; i < g.Slots; i += every {
			cx := center(i)
			c.stroke(cx, axisY, down, light, roleLine)
			s := fit(g.Marks[i].Label, every*slotW-1)
			x := min(max(cx-width(s)/2, px0), wd-width(s))
			c.text(x, labelY, s, roleText)
		}
	} else {
		next := 0
		for _, m := range g.Marks {
			cx := at(m.At)
			x := min(max(cx-width(m.Label)/2, 0), wd-width(m.Label))
			if x < next {
				continue
			}
			c.stroke(cx, axisY, down, light, roleLine)
			c.text(x, labelY, m.Label, roleText)
			next = x + width(m.Label) + 1
		}
	}
	for i, line := range categoryTitle {
		c.text(px0+(pw-width(line))/2, labelY+1+i, line, roleHead)
	}

	// The bars of each series side by side in the middle of their slots.
	// A value stands at the middle of a row, as its tick label does, so a
	// bar grows from the middle of the row of the base: its first cell is
	// a half block, and the axis line runs on into it.
	thick := max(1, int(math.Round(float64(slotW)*0.7))/max(bars, 1))
	group := thick * bars
	rb := int(math.Round(math.Min(math.Max(g.Base, 0), 1) * float64(xyRows)))
	baseY := axisY - rb
	k := 0
	for si, s := range g.Series {
		if !s.Bar {
			continue
		}
		role := plot.SeriesRole(si)
		up, down := barParts(slotValues(g, s), g.Base)
		// tuios.dev/plot's columns, a column a slot, measured from the middle
		// of the base row: up from it, and down from it, drawn up and turned.
		upG := plot.Bar{Values: up, Min: 0, Max: 1, Role: role, Half: true}.Draw(g.Slots, xyRows-rb+1, plot.Braille)
		var downG *plot.Grid
		if rb > 0 {
			downG = plot.Bar{Values: down, Min: 0, Max: 1, Role: role, Half: true}.Draw(g.Slots, rb+1, plot.Braille)
		}
		for i := 0; i < g.Slots; i++ {
			x0 := center(i) - group/2 + k*thick
			for dx := 0; dx < thick; dx++ {
				for r := 0; r < upG.H; r++ {
					if cell := upG.At(i, r); cell.Text != "" && cell.Text != " " {
						c.put(x0+dx, baseY-(upG.H-1-r), []rune(cell.Text)[0], canvasRole(cell.Role))
					}
				}
				if downG == nil {
					continue
				}
				for r := 0; r < downG.H; r++ {
					if cell := downG.At(i, r); cell.Text != "" && cell.Text != " " {
						c.put(x0+dx, baseY+(downG.H-1-r), turnedDown([]rune(cell.Text)[0]), canvasRole(cell.Role))
					}
				}
			}
		}
		k++
	}

	// Lines over the bars. Each run of points without a gap is drawn into a
	// grid from its first point to its last, so the points sit on their slots.
	col := func(a float64) int {
		if g.Band || bars > 0 {
			return center(min(max(int(a*float64(g.Slots)), 0), g.Slots-1))
		}
		return px0 + min(max(int(math.Round(a*float64(pw-1))), 0), pw-1)
	}
	for si, s := range g.Series {
		if s.Bar {
			continue
		}
		for _, run := range runs(s.Points) {
			c0, c1 := col(run[0].At), col(run[len(run)-1].At)
			if c1 < c0 {
				continue
			}
			vals := make([]float64, len(run))
			for i, p := range run {
				vals[i] = p.Value
			}
			// The grid takes the axis row as well, and its range runs past 0
			// and 1 by a dot and a half, so that 0 falls in the middle of the
			// axis row and 1 in the middle of the top row, where their
			// ticks are.
			d := float64(4 * xyRows)
			lg := plot.Line{Series: []plot.Series{{Values: vals, Role: plot.SeriesRole(si)}}, Min: -1.5 / d, Max: (d + 1.5) / d}.Draw(c1-c0+1, xyRows+1, plot.Braille)
			for y := 0; y < lg.H; y++ {
				for x := 0; x < lg.W; x++ {
					cell := lg.At(x, y)
					if cell.Text == "" {
						continue
					}
					r := []rune(cell.Text)[0]
					p := c.at(c0+x, top+y)
					if p == nil {
						continue
					}
					if p.r == '█' && p.role >= roleSeries && r >= 0x2800 && r <= 0x28ff {
						// A line crosses a bar: its dots on the bar's colour.
						c.putBg(c0+x, top+y, r, canvasRole(cell.Role), p.role)
						continue
					}
					if barGlyph(p.r) {
						// A line passes behind the end of a bar: a dot in place
						// of the bar's last block would change the height the
						// bar shows.
						continue
					}
					if p.r >= 0x2800 && p.r <= 0x28ff && p.role == canvasRole(cell.Role) {
						// Two runs of one series meet: keep the dots of both.
						// Another series takes the cell, in its own colour.
						r |= p.r
					}
					c.put(c0+x, top+y, r, canvasRole(cell.Role))
				}
			}
		}
	}

	// Labels last, where they cover nothing: the value above the end of
	// each bar, and the label of a line point next to the point: above it,
	// beside it or below it.
	// A label with no room is left out. Labels may use the free row above the
	// plot, not the axis or the rows below it.
	k = 0
	for _, s := range g.Series {
		if !s.Bar {
			continue
		}
		for _, p := range s.Points {
			if p.Label == "" {
				continue
			}
			i := min(max(int(p.At*float64(g.Slots)), 0), g.Slots-1)
			x0 := center(i) - group/2 + k*thick
			// The row past the end of the bar, up from the base or down from it.
			step := -1
			y := top + xyRows - rb - 1
			if p.Value < g.Base {
				step, y = 1, top+xyRows-rb
			}
			for y >= top && y < axisY && c.bar(x0, y) {
				y += step
			}
			if y >= top-1 && y < axisY {
				c.label(p.Label, top-1, axisY, [2]int{x0 + (thick-width(p.Label))/2, y})
			}
		}
		k++
	}
	for _, s := range g.Series {
		if s.Bar {
			continue
		}
		for _, p := range s.Points {
			if p.Label == "" || !(p.Value >= 0 && p.Value <= 1) {
				continue
			}
			x := col(p.At)
			d := float64(4 * xyRows)
			y := top + int(math.Round(d+1.5-p.Value*d))/4
			lw := width(p.Label)
			mid := min(max(x-lw/2, px0), wd-lw)
			c.label(p.Label, top-1, axisY, [2]int{mid, y - 1}, [2]int{x + 1, y - 1}, [2]int{x - lw, y - 1},
				[2]int{x + 2, y}, [2]int{x - 1 - lw, y}, [2]int{mid, y + 1})
		}
	}
	return c, nil
}

// turnedDown is the block a bar that hangs down from its base shows for the
// block plot draws for a bar that stands up: the same length from the
// other end of the cell.
func turnedDown(r rune) rune {
	switch {
	case r == '▀':
		return '▄'
	case r >= '▁' && r <= '█':
		return hanging(int(r - '▀'))
	}
	return r
}

// turnedLeft is the block a bar that runs left from its base shows for the
// block plot draws for a bar that runs right.
func turnedLeft(r rune) rune {
	switch {
	case r == '▐':
		return '▌'
	case r == '█':
		return '█'
	case r >= '▉' && r <= '▏':
		return leftward(int('▐' - r))
	}
	return r
}

// bar reports whether the cell at x, y holds a block of a bar.
func (c *canvas) bar(x, y int) bool {
	p := c.at(x, y)
	return p != nil && barGlyph(p.r)
}

// blank reports whether the cells from x to x+w-1 of row y are in the canvas
// and empty: no character, no stroke, no braille dots.
func (c *canvas) blank(x, y, w int) bool {
	for i := x; i < x+w; i++ {
		p := c.at(i, y)
		if p == nil || p.wide || p.mask != 0 || (p.r != 0 && p.r != ' ' && p.r != 0x2800) {
			return false
		}
	}
	return true
}

// label writes s at the first spot, from rows y0 to y1-1, where it covers
// nothing and has a free cell or the edge on each side, so that it does not
// run into other text. It reports whether s has a place.
func (c *canvas) label(s string, y0, y1 int, spots ...[2]int) bool {
	w := width(s)
	edge := func(x, y int) bool { return c.at(x, y) == nil || c.blank(x, y, 1) }
	for _, sp := range spots {
		x, y := sp[0], sp[1]
		if y < y0 || y >= y1 || !c.blank(x, y, w) || !edge(x-1, y) || !edge(x+w, y) {
			continue
		}
		c.text(x, y, s, roleText)
		return true
	}
	return false
}

// widen gives a chart w cells wide room for its title and legend on one line,
// within the columns there are.
func widen(w int, g *types.GeoXY, opt Options) int {
	want := widest(g.Title)
	legend := 0
	for _, s := range g.Series {
		if s.Name != "" {
			legend += width(s.Name) + 5
		}
	}
	want = max(want, legend-3)
	if opt.Cols > 0 {
		want = min(want, opt.Cols)
	}
	return max(w, want)
}

// runs splits the points of a line where a value is missing.
func runs(points []types.GeoPoint) [][]types.GeoPoint {
	var out [][]types.GeoPoint
	start := 0
	for i := range points {
		if i > 0 && points[i].Gap {
			out = append(out, points[start:i])
			start = i
		}
	}
	if start < len(points) {
		out = append(out, points[start:])
	}
	return out
}

// xyAcross draws the categories down the left, one slot of rows each: a row per
// bar series, and a row for the values of the lines as dots.
func xyAcross(g *types.GeoXY, opt Options) (*canvas, error) {
	if g.Slots <= 0 {
		return nil, ErrUnsupported
	}
	bars, lines := 0, 0
	for _, s := range g.Series {
		if s.Bar {
			bars++
		} else {
			lines++
		}
	}
	rows := bars
	if lines > 0 {
		rows++
	}
	rows = max(rows, 1)
	// A blank row between categories, so bars on rows next to each other
	// stay apart.
	sep := 1
	label := 0
	for _, m := range g.Marks {
		label = max(label, width(m.Label))
	}
	if opt.Cols > 0 {
		label = min(label, max(6, opt.Cols*2/5))
	} else {
		label = min(label, 40)
	}
	axisX := label + 1
	if label == 0 {
		axisX = 0
	}
	px0 := axisX + 1
	pw := 48
	if opt.Cols > 0 {
		pw = min(opt.Cols-px0-1, 64)
	}
	if pw < 8 {
		return nil, ErrTooWide
	}
	wd := widen(px0+pw+1, g, opt)
	var title []string
	for _, line := range g.Title {
		title = append(title, wrap(line, wd)...)
	}
	legend, legendH := xyLegend(g, wd)
	y := 0
	if len(title) > 0 {
		y = len(title) + 1
	}
	legendY := y
	if legendH > 0 {
		y += legendH + 1
	}
	categoryTitleY := y
	if g.CategoryTitle != "" {
		y++
	}
	top := y
	axisY := top + g.Slots*(rows+sep) - sep
	labelY := axisY + 1
	height := labelY + 1
	var valueTitle []string
	if g.ValueTitle != "" {
		valueTitle = wrap(g.ValueTitle, wd)
		height += len(valueTitle)
	}
	c, err := newCanvas(wd, height)
	if err != nil {
		return nil, err
	}
	for i, line := range title {
		c.text((wd-width(line))/2, i, line, roleTitle)
	}
	c.legend(legend, 0, legendY)
	if g.CategoryTitle != "" {
		c.text(0, categoryTitleY, fit(g.CategoryTitle, wd), roleHead)
	}
	c.vline(axisX, top, axisY, light, roleLine)
	c.hline(axisX, px0+pw-1, axisY, light, roleLine)
	rowOf := func(a float64) int {
		return top + min(max(int(a*float64(g.Slots)), 0), g.Slots-1)*(rows+sep)
	}
	if g.Band {
		for i, m := range g.Marks {
			s := fit(m.Label, label)
			c.text(label-width(s), top+i*(rows+sep)+(rows-1)/2, s, roleText)
		}
	} else {
		for _, m := range g.Marks {
			s := fit(m.Label, label)
			c.text(label-width(s), rowOf(m.At), s, roleText)
		}
	}
	next := 0
	for _, t := range g.Ticks {
		if !(t.At >= 0 && t.At <= 1) {
			continue
		}
		cx := axisX + min(int(math.Round(t.At*float64(pw))), pw)
		x := min(max(cx-width(t.Label)/2, 0), wd-width(t.Label))
		if x < next {
			continue
		}
		c.stroke(cx, axisY, down, light, roleLine)
		c.text(x, labelY, t.Label, roleText)
		next = x + width(t.Label) + 1
	}
	for i, line := range valueTitle {
		c.text(px0+(pw-width(line))/2, labelY+1+i, line, roleHead)
	}

	// A value stands at the middle of a column, as its tick does, and a bar
	// grows from the middle of the column of the base: its first cell is a
	// half block over the axis line.
	cb := int(math.Round(math.Min(math.Max(g.Base, 0), 1) * float64(pw)))
	baseX := axisX + cb
	k := 0
	for si, s := range g.Series {
		if !s.Bar {
			continue
		}
		role := plot.SeriesRole(si)
		up, down := barParts(slotValues(g, s), g.Base)
		// tuios.dev/plot's bars across, a row a slot, measured from the middle
		// of the base column: right from it, and left from it, drawn right and
		// turned.
		none := func(float64) string { return "" }
		rightG := plot.Bar{Values: up, Min: 0, Max: 1, Role: role, Horizontal: true, Format: none, Half: true}.Draw(pw-cb+1, g.Slots, plot.Braille)
		var leftG *plot.Grid
		if cb > 0 {
			leftG = plot.Bar{Values: down, Min: 0, Max: 1, Role: role, Horizontal: true, Format: none, Half: true}.Draw(cb+1, g.Slots, plot.Braille)
		}
		for i := 0; i < g.Slots; i++ {
			ry := top + i*(rows+sep) + k
			for x := 0; x < rightG.W; x++ {
				if cell := rightG.At(x, i); cell.Text != "" && cell.Text != " " {
					c.put(baseX+x, ry, []rune(cell.Text)[0], canvasRole(cell.Role))
				}
			}
			if leftG == nil {
				continue
			}
			for x := 0; x < leftG.W; x++ {
				if cell := leftG.At(x, i); cell.Text != "" && cell.Text != " " {
					c.put(baseX-x, ry, turnedLeft([]rune(cell.Text)[0]), canvasRole(cell.Role))
				}
			}
		}
		k++
	}
	for si, s := range g.Series {
		if s.Bar {
			continue
		}
		for _, p := range s.Points {
			if !(p.Value >= 0 && p.Value <= 1) {
				continue
			}
			x := axisX + min(int(math.Round(p.Value*float64(pw))), pw)
			c.put(x, rowOf(p.At)+bars, '●', seriesRole(si))
		}
	}

	// Labels last, where they cover nothing: the value past the end of each
	// bar, and the label of a line point beside its dot. A label with no room
	// is left out.
	k = 0
	for _, s := range g.Series {
		if !s.Bar {
			continue
		}
		for _, p := range s.Points {
			if p.Label == "" {
				continue
			}
			i := min(max(int(p.At*float64(g.Slots)), 0), g.Slots-1)
			ry := top + i*(rows+sep) + k
			lw := width(p.Label)
			if p.Value < g.Base {
				x := baseX
				for x >= axisX && c.bar(x, ry) {
					x--
				}
				// Past the end of the bar, or else on the other side of the base.
				c.label(p.Label, top, axisY, [2]int{x - lw, ry}, [2]int{baseX + 2, ry})
			} else {
				x := baseX
				for x <= axisX+pw && c.bar(x, ry) {
					x++
				}
				c.label(p.Label, top, axisY, [2]int{x + 1, ry})
			}
		}
		k++
	}
	for _, s := range g.Series {
		if s.Bar {
			continue
		}
		for _, p := range s.Points {
			if p.Label == "" || !(p.Value >= 0 && p.Value <= 1) {
				continue
			}
			x := axisX + min(int(math.Round(p.Value*float64(pw))), pw)
			y := rowOf(p.At) + bars
			c.label(p.Label, top, axisY, [2]int{x + 2, y}, [2]int{x - 1 - width(p.Label), y})
		}
	}
	return c, nil
}
