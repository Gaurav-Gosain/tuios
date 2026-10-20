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
	return roleLine
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

// The eighth blocks plot draws, and the blocks that hang down or stand to the
// left that a bar on the other side of the base takes instead.
var eighthUp = map[string]int{"▁": 1, "▂": 2, "▃": 3, "▄": 4, "▅": 5, "▆": 6, "▇": 7, "█": 8}
var eighthRight = map[string]int{"▏": 1, "▎": 2, "▍": 3, "▌": 4, "▋": 5, "▊": 6, "▉": 7, "█": 8}

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
	slotW := min(max(label+2, least, 4), 12)
	if opt.Cols > 0 {
		avail := opt.Cols - px0 - 1
		slotW = min(max(slotW, 8), avail/g.Slots)
		if slotW < least {
			return nil, ErrTooWide
		}
	}
	pw := g.Slots * slotW
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
	thick := max(1, int(math.Round(float64(slotW)*0.7))/max(bars, 1))
	group := thick * bars
	rb := int(math.Round(math.Min(math.Max(g.Base, 0), 1) * float64(xyRows)))
	k := 0
	for si, s := range g.Series {
		if !s.Bar {
			continue
		}
		role := plot.SeriesRole(si)
		up, down := barParts(slotValues(g, s), g.Base)
		var upG, downG *plot.Grid
		if xyRows-rb > 0 {
			upG = plot.Bar{Values: up, Min: 0, Max: 1, Role: role}.Draw(g.Slots, xyRows-rb, plot.Braille)
		}
		if rb > 0 {
			downG = plot.Bar{Values: down, Min: 0, Max: 1, Role: role}.Draw(g.Slots, rb, plot.Braille)
		}
		for i := 0; i < g.Slots; i++ {
			x0 := center(i) - group/2 + k*thick
			for dx := 0; dx < thick; dx++ {
				if upG != nil {
					for r := 0; r < upG.H; r++ {
						cell := upG.At(i, r)
						if cell.Text != "" && cell.Text != " " {
							c.put(x0+dx, top+r, []rune(cell.Text)[0], canvasRole(cell.Role))
						}
					}
				}
				if downG != nil {
					for r := 0; r < downG.H; r++ {
						cell := downG.At(i, r)
						if lv := eighthUp[cell.Text]; lv > 0 {
							c.put(x0+dx, top+xyRows-rb+(downG.H-1-r), hanging(lv), canvasRole(cell.Role))
						}
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
			lg := plot.Line{Series: []plot.Series{{Values: vals, Role: plot.SeriesRole(si)}}, Min: 0, Max: 1}.Draw(c1-c0+1, xyRows, plot.Braille)
			for y := 0; y < lg.H; y++ {
				for x := 0; x < lg.W; x++ {
					cell := lg.At(x, y)
					if cell.Text == "" {
						continue
					}
					r := []rune(cell.Text)[0]
					p := c.at(c0+x, top+y)
					if p == nil || barGlyph(p.r) {
						// A line passes behind a bar: a cell cannot show both,
						// and a line dot in place of the bar's top block changes
						// the height the bar shows.
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
	return c, nil
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
	sep := 0
	if rows > 1 {
		sep = 1
	}
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
		cx := px0 + min(int(math.Round(t.At*float64(pw))), pw-1)
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

	cb := int(math.Round(math.Min(math.Max(g.Base, 0), 1) * float64(pw)))
	none := func(float64) string { return "" }
	k := 0
	for si, s := range g.Series {
		if !s.Bar {
			continue
		}
		role := plot.SeriesRole(si)
		up, down := barParts(slotValues(g, s), g.Base)
		var upG, downG *plot.Grid
		if pw-cb > 0 {
			upG = plot.Bar{Values: up, Min: 0, Max: 1, Role: role, Horizontal: true, Format: none}.Draw(pw-cb, g.Slots, plot.Braille)
		}
		if cb > 0 {
			downG = plot.Bar{Values: down, Min: 0, Max: 1, Role: role, Horizontal: true, Format: none}.Draw(cb, g.Slots, plot.Braille)
		}
		for i := 0; i < g.Slots; i++ {
			ry := top + i*(rows+sep) + k
			if upG != nil {
				for x := 0; x < upG.W; x++ {
					cell := upG.At(x, i)
					if cell.Text != "" && cell.Text != " " {
						c.put(px0+cb+x, ry, []rune(cell.Text)[0], canvasRole(cell.Role))
					}
				}
			}
			if downG != nil {
				for x := 0; x < downG.W; x++ {
					cell := downG.At(x, i)
					if lv := eighthRight[cell.Text]; lv > 0 {
						c.put(px0+cb-1-x, ry, leftward(lv), canvasRole(cell.Role))
					}
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
			x := px0 + min(int(math.Round(p.Value*float64(pw-1))), pw-1)
			c.put(x, rowOf(p.At)+bars, '●', seriesRole(si))
		}
	}
	return c, nil
}
