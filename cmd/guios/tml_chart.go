package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/Gaurav-Gosain/mygo/ui"
	"tuios.dev/loom"
	"tuios.dev/plot"
	"tuios.dev/tml"
)

// The charts of a TML surface, drawn as shapes rather than cells. The rows
// are read with loom's chart readers, so a chart shows the same values,
// labels and colours as in a terminal pane. The sizes follow loom's rows,
// in tmlCellH each, unless the element sets h.

// chartPie is the most slices a pie draws: the rest fold into "other",
// as plot.Pie folds them.
const chartPie = 12

// maxChartSeries bounds the y fields of a line or an area, as loom does.
const maxChartSeries = 12

// chartColor is the colour of a plot role: a series, a heat step, the
// track, or a TML role, with a fade toward the card.
func (r *tmlDraw) chartColor(role plot.Role, def ui.Color) ui.Color {
	pal := r.pal
	base, k := role.Faded()
	var col ui.Color
	switch base {
	case plot.None:
		col = def
	case plot.Series5:
		col = pal.accent.Mix(pal.danger, 0.5)
	case plot.Series6:
		col = pal.done.Mix(pal.accent, 0.5)
	case plot.Heat1, plot.Heat2, plot.Heat3, plot.Heat4, plot.Heat5:
		step := float32(base[len(base)-1]-'1') / 4
		col = pal.card.Mix(pal.accent, 0.16+0.84*step)
	case plot.Track:
		col = chartTrack(pal)
	default:
		col = r.role(string(base), def)
	}
	if k > 0 {
		col = col.Mix(pal.card, 0.2*float32(k))
	}
	return col
}

// chartTrack is the empty part of a gauge or a bar, and the land of a
// map: a step from the card toward the text, which shows on a light card
// as on a dark one.
func chartTrack(pal *palette) ui.Color { return pal.card.Mix(pal.text, 0.09) }

// chartLabel draws s at x, y: align 0 starts there, 1 ends there and 0.5
// centres on it. It returns the width.
func chartLabel(p *ui.Painter, x, y float32, s string, size float32, col ui.Color, align float32) float32 {
	sp := ui.Span{Text: s, Size: size, Color: col}
	w, _ := p.MeasureText(0, sp)
	p.RichText(x-w*align, y, 0, sp)
	return w
}

// chartRange is the low and high end of the finite values, with 0 inside
// it for a chart drawn up from a base.
func chartRange(withZero bool, vals ...[]float64) (lo, hi float64, ok bool) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for _, vs := range vals {
		for _, v := range vs {
			if !math.IsNaN(v) && !math.IsInf(v, 0) {
				lo, hi, ok = min(lo, v), max(hi, v), true
			}
		}
	}
	if !ok {
		return 0, 0, false
	}
	if withZero {
		lo, hi = min(lo, 0), max(hi, 0)
	}
	if hi == lo {
		hi = lo + 1
	}
	return lo, hi, true
}

func finiteNum(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// chart draws one chart element: its title and legend, then its body.
func (r *tmlDraw) chart(n *tml.Node, ptr string, vars map[string]any) {
	c, pal := r.c, r.pal
	title := r.str(n, "title", vars)
	role := plot.Role(r.str(n, "role", vars))
	switch n.Type {
	case "sparkline":
		r.sparkline(n, ptr, vars, title, role)
		return
	case "gauge":
		r.gauge(n, ptr, vars, title, role)
		return
	}
	var legend []plot.LegendItem
	var body func(ui.Element) ui.Element
	rows := 6
	switch n.Type {
	case "bar":
		data := r.data(n, "data", vars)
		vals := loom.ChartValues(data, n.AttrString("y"))
		labels := loom.ChartLabels(data, n.AttrString("x"))
		roles, keys := loom.ChartRowRoles(data)
		legend = keys
		horiz := n.AttrString("dir") == "h"
		if horiz {
			rows = max(len(vals), 1)
		}
		body = func(e ui.Element) ui.Element { return r.barChart(e, vals, labels, roles, role, horiz) }
	case "line", "area":
		data := r.data(n, "data", vars)
		var series []plot.Series
		fields := strings.Split(n.AttrString("y"), ",")
		if len(fields) > maxChartSeries {
			fields = fields[:maxChartSeries]
		}
		for i, f := range fields {
			f = strings.TrimSpace(f)
			sr := plot.Series{Name: f, Values: loom.ChartValues(data, f), Role: plot.SeriesRole(i)}
			if i == 0 && role != "" {
				sr.Role = role
			}
			series = append(series, sr)
		}
		if len(series) > 1 {
			for _, s := range series {
				legend = append(legend, plot.LegendItem{Label: s.Name, Role: s.Role})
			}
		}
		labels := loom.ChartLabels(data, n.AttrString("x"))
		axis := n.Type == "area" || r.truthyAttr(n, "axis", vars)
		body = func(e ui.Element) ui.Element { return r.lineChart(e, series, labels, n.Type == "area", axis) }
	case "histogram":
		vals := loom.ChartValues(r.data(n, "values", vars), "")
		bins := int(r.num(n, "bins", vars, 0))
		rows = 5
		body = func(e ui.Element) ui.Element { return r.histogram(e, vals, bins, role) }
	case "heatmap":
		grid, rowL, colL := loom.ChartGrid(r.data(n, "data", vars), n.AttrString("x"), n.AttrString("y"), n.AttrString("v"))
		rows = len(rowL) + 2
		body = func(e ui.Element) ui.Element { return r.heatmap(e, grid, rowL, colL) }
	case "pie":
		data := r.data(n, "data", vars)
		vals := loom.ChartValues(data, n.AttrString("v"))
		labels := loom.ChartLabels(data, n.AttrString("label"))
		rows = 7
		donut := n.AttrString("style") == "donut"
		body = func(e ui.Element) ui.Element { return r.pie(e, vals, labels, donut) }
	case "map":
		r.worldMap(n, ptr, vars, title)
		return
	}
	col := r.box(ui.Column(c.Key(ptr)), n, vars).Gap(6).AlignItems(ui.Stretch)
	col.Children(func() {
		if title != "" || len(legend) > 0 {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, title).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.textMuted).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				r.chartLegend(legend)
			})
		}
		e := ui.Box(c.Key("chart"))
		if _, set := n.Attr("h"); set {
			e = e.Grow(1).MinHeight(tmlCellH)
		} else {
			e = e.Height(float32(rows) * tmlCellH)
		}
		body(e)
	})
}

// truthyAttr is a flag attribute, false when absent.
func (r *tmlDraw) truthyAttr(n *tml.Node, attr string, vars map[string]any) bool {
	v, ok := r.value(n, attr, vars)
	return ok && tml.Truthy(v)
}

// chartLegend draws legend items in a row: a swatch and its label.
func (r *tmlDraw) chartLegend(items []plot.LegendItem) {
	c, pal := r.c, r.pal
	for i, it := range items {
		ui.Row(c.Key(i)).Gap(5).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(8, 8).Radius(2).Background(r.chartColor(it.Role, pal.accent))
			ui.Text(c, it.Label).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine()
		})
	}
}

// yAxis draws the value labels and guide lines of a plot area, the top,
// the middle and the base, and returns the room the labels take at the
// left.
func (r *tmlDraw) yAxis(p *ui.Painter, b ui.Rect, lo, hi float64, format func(float64) string) float32 {
	pal := r.pal
	labels := []float64{hi, (lo + hi) / 2, lo}
	w := float32(0)
	for _, v := range labels {
		mw, _ := p.MeasureText(0, ui.Span{Text: format(v), Size: fsCaption})
		w = max(w, mw)
	}
	gap := w + 8
	for i, v := range labels {
		y := b.Y + b.H*float32(i)/2
		p.Fill(ui.Rect{X: b.X + gap, Y: y, W: b.W - gap, H: 1}, pal.divider.Alpha(0.6), 0)
		chartLabel(p, b.X+w, y-fsCaption*0.65, format(v), fsCaption, pal.textFaint, 1)
	}
	return gap
}

// barChart draws columns (or bars with horiz) up from zero, each in its
// row's role, its label under it and its value over it where it fits.
func (r *tmlDraw) barChart(e ui.Element, vals []float64, labels []string, roles []plot.Role, role plot.Role, horiz bool) ui.Element {
	pal := r.pal
	hx, _, over := e.PointerPosition()
	hy := float32(0)
	if horiz {
		_, hy, _ = e.PointerPosition()
	}
	roleOf := func(i int) ui.Color {
		if i < len(roles) && roles[i] != plot.None {
			return r.chartColor(roles[i], pal.accent)
		}
		return r.chartColor(role, pal.accent)
	}
	label := func(i int) string {
		if i < len(labels) {
			return labels[i]
		}
		return ""
	}
	return e.Draw(func(p *ui.Painter, b ui.Rect) {
		lo, hi, ok := chartRange(true, vals)
		if !ok || len(vals) == 0 {
			return
		}
		if horiz {
			lw := float32(0)
			for i := range vals {
				w, _ := p.MeasureText(0, ui.Span{Text: label(i), Size: fsSmall})
				lw = max(lw, w)
			}
			lw = min(lw, b.W/3)
			vw := float32(0)
			for _, v := range vals {
				w, _ := p.MeasureText(0, ui.Span{Text: plot.Compact(v), Size: fsSmall})
				vw = max(vw, w)
			}
			rowH := b.H / float32(len(vals))
			track := ui.Rect{X: b.X + lw + 10, W: max(b.W-lw-vw-20, 1)}
			for i, v := range vals {
				y := b.Y + rowH*float32(i)
				hot := over && hy >= y-b.Y && hy < y-b.Y+rowH
				p.Clip(ui.Rect{X: b.X, Y: y, W: lw, H: rowH}, 0, func() {
					chartLabel(p, b.X, y+(rowH-fsSmall*1.3)/2, label(i), fsSmall, pal.textMuted, 0)
				})
				bh := min(rowH*0.5, 10)
				by := y + (rowH-bh)/2
				p.Fill(ui.Rect{X: track.X, Y: by, W: track.W, H: bh}, chartTrack(pal), bh/2)
				if finiteNum(v) && v > lo {
					w := track.W * float32((v-lo)/(hi-lo))
					col := roleOf(i)
					if hot {
						col = col.Mix(pal.text, 0.15)
					}
					p.Fill(ui.Rect{X: track.X, Y: by, W: max(w, bh), H: bh}, col, bh/2)
				}
				chartLabel(p, track.X+track.W+10, y+(rowH-fsSmall*1.3)/2, plot.Compact(v), fsSmall, pal.text, 0)
			}
			return
		}
		labelH := float32(fsSmall * 1.6)
		plotR := ui.Rect{X: b.X, Y: b.Y + fsCaption, W: b.W, H: b.H - labelH - fsCaption}
		axis := b.H >= 4*tmlCellH && b.W >= 16*tmlCellW
		if axis {
			gap := r.yAxis(p, plotR, lo, hi, plot.Compact)
			plotR.X += gap
			plotR.W -= gap
		}
		slot := plotR.W / float32(len(vals))
		zeroY := plotR.Y + plotR.H*float32(hi/(hi-lo))
		for i, v := range vals {
			x := plotR.X + slot*float32(i)
			hot := over && hx >= x-b.X && hx < x-b.X+slot
			bw := max(min(slot*0.62, 48), 1)
			bx := x + (slot-bw)/2
			if finiteNum(v) {
				h := plotR.H * float32(math.Abs(v)/(hi-lo))
				y := zeroY - h
				if v < 0 {
					y = zeroY
				}
				col := roleOf(i)
				if hot {
					col = col.Mix(pal.text, 0.15)
				}
				p.Fill(ui.Rect{X: bx, Y: y, W: bw, H: max(h, 1)}, col, min(3, bw/2))
				s := plot.Compact(v)
				if w, _ := p.MeasureText(0, ui.Span{Text: s, Size: fsCaption}); w <= slot-2 || hot {
					ty := y - fsCaption*1.35
					if v < 0 {
						ty = y + h + 2
					}
					tc := pal.textMuted
					if hot {
						tc = pal.text
					}
					chartLabel(p, x+slot/2, ty, s, fsCaption, tc, 0.5)
				}
			}
			if l := label(i); l != "" {
				p.Clip(ui.Rect{X: x, Y: b.Y + b.H - labelH, W: slot, H: labelH}, 0, func() {
					chartLabel(p, x+slot/2, b.Y+b.H-labelH+3, l, fsSmall, pal.textMuted, 0.5)
				})
			}
		}
		p.Fill(ui.Rect{X: plotR.X, Y: zeroY, W: plotR.W, H: 1}, pal.textFaint.Alpha(0.5), 0)
	})
}

// lineChart draws each series as a line over evenly spaced points, filled
// down to the base for an area. A missing value breaks the line. The
// pointer shows the values at the nearest point.
func (r *tmlDraw) lineChart(e ui.Element, series []plot.Series, labels []string, fill, axis bool) ui.Element {
	pal := r.pal
	hx, _, over := e.PointerPosition()
	n := 0
	var all [][]float64
	for _, s := range series {
		n = max(n, len(s.Values))
		all = append(all, s.Values)
	}
	return e.Draw(func(p *ui.Painter, b ui.Rect) {
		lo, hi, ok := chartRange(fill, all...)
		if !ok || n == 0 {
			return
		}
		plotR := b
		if axis {
			plotR.H -= fsSmall * 1.6
			gap := r.yAxis(p, plotR, lo, hi, plot.Compact)
			plotR.X += gap
			plotR.W -= gap
			if len(labels) > 0 {
				ly := plotR.Y + plotR.H + 4
				chartLabel(p, plotR.X, ly, labels[0], fsCaption, pal.textFaint, 0)
				if len(labels) > 2 {
					chartLabel(p, plotR.X+plotR.W/2, ly, labels[len(labels)/2], fsCaption, pal.textFaint, 0.5)
				}
				if len(labels) > 1 {
					chartLabel(p, plotR.X+plotR.W, ly, labels[len(labels)-1], fsCaption, pal.textFaint, 1)
				}
			}
		}
		px := func(i int) float32 {
			if n == 1 {
				return plotR.X + plotR.W/2
			}
			return plotR.X + plotR.W*float32(i)/float32(n-1)
		}
		py := func(v float64) float32 { return plotR.Y + plotR.H*float32((hi-v)/(hi-lo)) }
		base := py(max(lo, min(0, hi)))
		for _, s := range series {
			col := r.chartColor(s.Role, pal.accent)
			var runs [][][2]float32
			var cur [][2]float32
			for i, v := range s.Values {
				if !finiteNum(v) {
					if len(cur) > 0 {
						runs = append(runs, cur)
						cur = nil
					}
					continue
				}
				cur = append(cur, [2]float32{px(i), py(v)})
			}
			if len(cur) > 0 {
				runs = append(runs, cur)
			}
			for _, run := range runs {
				if fill && len(run) > 1 {
					area := new(ui.Path).MoveTo(run[0][0], base)
					for _, pt := range run {
						area.LineTo(pt[0], pt[1])
					}
					area.LineTo(run[len(run)-1][0], base).Close()
					p.FillPathGradient(area, ui.LinearGradient{From: col.Alpha(0.32), To: col.Alpha(0.04), Angle: 180})
				}
				if len(run) == 1 {
					p.FillPath(new(ui.Path).Circle(run[0][0], run[0][1], 2), col)
					continue
				}
				line := new(ui.Path).MoveTo(run[0][0], run[0][1])
				for _, pt := range run[1:] {
					line.LineTo(pt[0], pt[1])
				}
				p.StrokePath(line, 1.75, col)
			}
		}
		if !over || hx < plotR.X-b.X || hx > plotR.X-b.X+plotR.W {
			return
		}
		i := 0
		if n > 1 {
			i = int(math.Round(float64((hx - (plotR.X - b.X)) / plotR.W * float32(n-1))))
			i = max(0, min(n-1, i))
		}
		x := px(i)
		p.Fill(ui.Rect{X: x, Y: plotR.Y, W: 1, H: plotR.H}, pal.textFaint.Alpha(0.6), 0)
		var rowsT []string
		var cols []ui.Color
		if i < len(labels) && labels[i] != "" {
			rowsT, cols = append(rowsT, labels[i]), append(cols, pal.textMuted)
		}
		for _, s := range series {
			if i >= len(s.Values) || !finiteNum(s.Values[i]) {
				continue
			}
			p.FillPath(new(ui.Path).Circle(x, py(s.Values[i]), 3.5), r.chartColor(s.Role, pal.accent))
			text := plot.Compact(s.Values[i])
			if len(series) > 1 {
				text = s.Name + " " + text
			}
			rowsT, cols = append(rowsT, text), append(cols, pal.text)
		}
		var lines []ui.Span
		for k, t := range rowsT {
			if k < len(rowsT)-1 {
				t += "\n"
			}
			lines = append(lines, ui.Span{Text: t, Size: fsSmall, Color: cols[k]})
		}
		r.chartTip(p, b, x, plotR.Y, lines)
	})
}

// chartTip draws a small card of spans beside x, inside b.
func (r *tmlDraw) chartTip(p *ui.Painter, b ui.Rect, x, y float32, spans []ui.Span) {
	if len(spans) == 0 {
		return
	}
	pal := r.pal
	w, h := p.MeasureText(0, spans...)
	box := ui.Rect{X: x + 10, Y: y, W: w + 16, H: h + 10}
	if box.X+box.W > b.X+b.W {
		box.X = x - 10 - box.W
	}
	p.Shadow(box, 6, 0, 2, 8, 0, pal.floatShadow)
	p.Fill(box, pal.popover, 6)
	p.Stroke(box, pal.edgeHigh, 6, 1)
	p.RichText(box.X+8, box.Y+5, 0, spans...)
}

// histogram counts the values into bins, with plot's counting, and draws
// the counts as touching columns with the range under them.
func (r *tmlDraw) histogram(e ui.Element, vals []float64, bins int, role plot.Role) ui.Element {
	pal := r.pal
	hx, _, over := e.PointerPosition()
	return e.Draw(func(p *ui.Painter, b ui.Rect) {
		k := bins
		if k <= 0 {
			k = min(20, max(1, int(b.W/tmlCellW)/2))
		}
		counts, lo, hi := plot.Histogram{Values: vals}.Counts(k)
		if counts == nil {
			return
		}
		top := 0.0
		for _, c := range counts {
			top = max(top, c)
		}
		if top == 0 {
			return
		}
		plotR := b
		plotR.H -= fsSmall * 1.6
		gap := r.yAxis(p, plotR, 0, top, func(v float64) string { return plot.Compact(math.Round(v)) })
		plotR.X += gap
		plotR.W -= gap
		colW := plotR.W / float32(len(counts))
		col := r.chartColor(role, pal.accent)
		for i, c := range counts {
			h := plotR.H * float32(c/top)
			x := plotR.X + colW*float32(i)
			fill := col
			if over && hx >= x-b.X && hx < x-b.X+colW {
				fill = col.Mix(pal.text, 0.15)
				span := (hi - lo) / float64(len(counts))
				defer r.chartTip(p, b, x+colW/2, plotR.Y, []ui.Span{
					{Text: plot.Compact(lo+span*float64(i)) + " to " + plot.Compact(lo+span*float64(i+1)) + "\n", Size: fsSmall, Color: pal.textMuted},
					{Text: plot.Compact(c), Size: fsSmall, Color: pal.text}})
			}
			if c > 0 {
				p.Fill(ui.Rect{X: x + 0.5, Y: plotR.Y + plotR.H - max(h, 1), W: max(colW-1, 1), H: max(h, 1)}, fill, min(2, colW/3))
			}
		}
		ly := plotR.Y + plotR.H + 4
		chartLabel(p, plotR.X, ly, plot.Compact(lo), fsCaption, pal.textFaint, 0)
		chartLabel(p, plotR.X+plotR.W, ly, plot.Compact(hi), fsCaption, pal.textFaint, 1)
	})
}

// heatmap draws a grid of cells shaded in five steps from the card to the
// accent, as loom's heat roles step, with the row labels at the left and
// the column labels under the grid.
func (r *tmlDraw) heatmap(e ui.Element, grid [][]float64, rowL, colL []string) ui.Element {
	pal := r.pal
	hx, hy, over := e.PointerPosition()
	return e.Draw(func(p *ui.Painter, b ui.Rect) {
		if len(grid) == 0 || len(colL) == 0 {
			return
		}
		var flat [][]float64
		flat = append(flat, grid...)
		lo, hi, ok := chartRange(false, flat...)
		if !ok {
			return
		}
		lw := float32(0)
		for _, l := range rowL {
			w, _ := p.MeasureText(0, ui.Span{Text: l, Size: fsSmall})
			lw = max(lw, w)
		}
		lw = min(lw+10, b.W/3)
		labelH := float32(fsSmall * 1.6)
		cell := ui.Rect{W: (b.W - lw) / float32(len(colL)), H: (b.H - labelH) / float32(len(rowL))}
		for y, row := range grid {
			cy := b.Y + cell.H*float32(y)
			p.Clip(ui.Rect{X: b.X, Y: cy, W: lw - 6, H: cell.H}, 0, func() {
				chartLabel(p, b.X, cy+(cell.H-fsSmall*1.3)/2, rowL[y], fsSmall, pal.textMuted, 0)
			})
			for x, v := range row {
				cx := b.X + lw + cell.W*float32(x)
				box := ui.Rect{X: cx + 1, Y: cy + 1, W: max(cell.W-2, 1), H: max(cell.H-2, 1)}
				if !finiteNum(v) {
					p.Stroke(box, pal.divider, 3, 1)
					continue
				}
				step := min(4, int((v-lo)/(hi-lo)*5))
				col := r.chartColor(plot.Role(fmt.Sprintf("heat%d", step+1)), pal.accent)
				p.Fill(box, col, 3)
				if over && hx >= cx-b.X && hx < cx-b.X+cell.W && hy >= cy-b.Y && hy < cy-b.Y+cell.H {
					p.Stroke(box, pal.text, 3, 1.5)
					defer r.chartTip(p, b, cx+cell.W, cy, []ui.Span{
						{Text: rowL[y] + ", " + colL[x] + "\n", Size: fsSmall, Color: pal.textMuted},
						{Text: plot.Compact(v), Size: fsSmall, Color: pal.text}})
				}
			}
		}
		// Every column label when they fit, else the first and the last.
		ly := b.Y + b.H - labelH + 3
		fits := true
		for _, l := range colL {
			if w, _ := p.MeasureText(0, ui.Span{Text: l, Size: fsCaption}); w > cell.W-4 {
				fits = false
				break
			}
		}
		for x, l := range colL {
			if !fits && x != 0 && x != len(colL)-1 {
				continue
			}
			switch {
			case fits:
				chartLabel(p, b.X+lw+cell.W*(float32(x)+0.5), ly, l, fsCaption, pal.textFaint, 0.5)
			case x == 0:
				chartLabel(p, b.X+lw, ly, l, fsCaption, pal.textFaint, 0)
			default:
				chartLabel(p, b.X+b.W, ly, l, fsCaption, pal.textFaint, 1)
			}
		}
	})
}

// pie draws the shares of a whole from twelve o'clock clockwise, with the
// legend at the right: each slice's label and share, as plot gives them.
func (r *tmlDraw) pie(e ui.Element, vals []float64, labels []string, donut bool) ui.Element {
	c, pal := r.c, r.pal
	legend := plot.Pie{Values: vals, Labels: labels}.Legend()
	// The slices past the twelfth fold into one, as in the legend.
	shares := make([]float64, 0, min(len(vals), chartPie))
	total := 0.0
	for i, v := range vals {
		if !finiteNum(v) || v < 0 {
			v = 0
		}
		total += v
		if i < chartPie {
			shares = append(shares, v)
		} else {
			shares[chartPie-1] += v
		}
	}
	return e.Children(func() {
		ui.Row(c).Gap(20).AlignItems(ui.Center).Height(float32(7) * tmlCellH).Children(func() {
			disc := ui.Box(c.Key("disc")).Size(7*tmlCellH, 7*tmlCellH).Shrink(0)
			disc.Draw(func(p *ui.Painter, b ui.Rect) {
				if total <= 0 {
					p.FillPath(new(ui.Path).Circle(b.X+b.W/2, b.Y+b.H/2, b.H/2-2), chartTrack(pal))
					return
				}
				cx, cy, rad := b.X+b.W/2, b.Y+b.H/2, b.H/2-2
				inner := float32(0)
				if donut {
					inner = rad * 0.58
				}
				at := -math.Pi / 2
				for i, v := range shares {
					if v <= 0 {
						continue
					}
					sweep := v / total * 2 * math.Pi
					role := plot.SeriesRole(i)
					if i < len(legend) {
						role = legend[i].Role
					}
					p.FillPath(arcPath(cx, cy, rad, inner, at, at+sweep), r.chartColor(role, pal.accent))
					at += sweep
				}
				// A thin card line between the slices keeps them apart.
				if len(shares) > 1 {
					at = -math.Pi / 2
					for _, v := range shares {
						if v <= 0 {
							continue
						}
						s, co := math.Sincos(at)
						edge := new(ui.Path).MoveTo(cx+inner*float32(co), cy+inner*float32(s)).LineTo(cx+(rad+1)*float32(co), cy+(rad+1)*float32(s)).Cap(ui.ButtCap)
						p.StrokePath(edge, 2, pal.card)
						at += v / total * 2 * math.Pi
					}
				}
			})
			ui.Column(c).Gap(4).Grow(1).MinWidth(0).Children(func() {
				r.chartLegendColumn(legend)
			})
		})
	})
}

// chartLegendColumn draws legend items one under the other.
func (r *tmlDraw) chartLegendColumn(items []plot.LegendItem) {
	c, pal := r.c, r.pal
	for i, it := range items {
		ui.Row(c.Key(i)).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(9, 9).Radius(2).Background(r.chartColor(it.Role, pal.accent)).Shrink(0)
			ui.Text(c, it.Label).FontSize(fsSmall).TextColor(pal.text).SingleLine().Ellipsis("…").MinWidth(0)
		})
	}
}

// arcPath is a slice of a disc from angle a0 to a1, or of a ring when
// inner is over 0.
func arcPath(cx, cy, rad, inner float32, a0, a1 float64) *ui.Path {
	steps := max(2, int(math.Ceil((a1-a0)/(math.Pi/48))))
	pt := func(rr float32, a float64) (float32, float32) {
		s, co := math.Sincos(a)
		return cx + rr*float32(co), cy + rr*float32(s)
	}
	path := new(ui.Path)
	if inner > 0 {
		x, y := pt(inner, a0)
		path.MoveTo(x, y)
	} else {
		path.MoveTo(cx, cy)
	}
	for i := 0; i <= steps; i++ {
		x, y := pt(rad, a0+(a1-a0)*float64(i)/float64(steps))
		path.LineTo(x, y)
	}
	if inner > 0 {
		for i := steps; i >= 0; i-- {
			x, y := pt(inner, a0+(a1-a0)*float64(i)/float64(steps))
			path.LineTo(x, y)
		}
	}
	return path.Close()
}

// sparkline draws a one-row trend: the title, the line and the last
// value, as loom lays it out.
func (r *tmlDraw) sparkline(n *tml.Node, ptr string, vars map[string]any, title string, role plot.Role) {
	c, pal := r.c, r.pal
	vals := loom.ChartValues(r.data(n, "values", vars), "")
	last := ""
	for i := len(vals) - 1; i >= 0; i-- {
		if finiteNum(vals[i]) {
			last = plot.Compact(vals[i])
			break
		}
	}
	col := r.chartColor(role, pal.accent)
	r.box(ui.Row(c.Key(ptr)), n, vars).Gap(8).AlignItems(ui.Center).Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine().Shrink(0)
		}
		w := max(float32(len(vals))*tmlCellW, 48)
		ui.Box(c.Key("spark")).Width(w).Height(tmlCellH).Shrink(1).MinWidth(24).Draw(func(p *ui.Painter, b ui.Rect) {
			lo, hi, ok := chartRange(false, vals)
			if !ok {
				return
			}
			b = ui.Rect{X: b.X + 1, Y: b.Y + 3, W: b.W - 2, H: b.H - 6}
			path := new(ui.Path)
			started := false
			var end [2]float32
			for i, v := range vals {
				if !finiteNum(v) {
					started = false
					continue
				}
				x := b.X + b.W*float32(i)/float32(max(len(vals)-1, 1))
				y := b.Y + b.H*float32((hi-v)/(hi-lo))
				if started {
					path.LineTo(x, y)
				} else {
					path.MoveTo(x, y)
					started = true
				}
				end = [2]float32{x, y}
			}
			p.StrokePath(path, 1.5, col)
			p.FillPath(new(ui.Path).Circle(end[0], end[1], 2.25), col)
		})
		if last != "" {
			ui.Text(c, last).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.text).SingleLine().Shrink(0)
		}
	})
}

// gauge draws a value against its maximum: the label, a meter and the
// share.
func (r *tmlDraw) gauge(n *tml.Node, ptr string, vars map[string]any, title string, role plot.Role) {
	c, pal := r.c, r.pal
	value := r.num(n, "value", vars, math.NaN())
	top := r.num(n, "max", vars, 100)
	frac := math.NaN()
	if finiteNum(value) && top > 0 {
		frac = min(max(value/top, 0), 1)
	}
	col := r.chartColor(role, pal.accent)
	r.box(ui.Column(c.Key(ptr)), n, vars).Gap(4).AlignItems(ui.Stretch).Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.textMuted).SingleLine()
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if l := r.str(n, "label", vars); l != "" {
				ui.Text(c, l).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine().Shrink(0)
			}
			ui.Box(c).Height(8).Radius(4).Background(chartTrack(pal)).Grow(1).MinWidth(40).Clip().Children(func() {
				if !math.IsNaN(frac) {
					ui.Box(c).Height(8).Radius(4).Background(col).WidthPercent(float32(frac * 100))
				}
			})
			share := "-"
			if !math.IsNaN(frac) {
				share = plot.Percent(frac)
			}
			ui.Text(c, share).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.text).SingleLine().Shrink(0)
		})
	})
}

// mapPitch is the side of one map pixel in DIPs: plot draws the world in
// half blocks, two square pixels to a cell, and each is a dot here.
const mapPitch = 3

// worldMap draws the countries shaded by value, with plot's map and its
// legend, as dots on the card.
func (r *tmlDraw) worldMap(n *tml.Node, ptr string, vars map[string]any, title string) {
	c, pal := r.c, r.pal
	data := r.data(n, "data", vars)
	vals := loom.ChartValues(data, n.AttrString("y"))
	keys := loom.ChartLabels(data, n.AttrString("x"))
	codes := map[string]float64{}
	for i, k := range keys {
		code := strings.ToUpper(strings.TrimSpace(k))
		if len(code) != 2 || i >= len(vals) || !finiteNum(vals[i]) {
			continue
		}
		codes[code] = vals[i]
	}
	mp := plot.Map{Values: codes, Log: r.truthyAttr(n, "log", vars), Aspect: 2}
	if unit := r.str(n, "unit", vars); unit != "" {
		mp.Format = func(v float64) string { return unit + plot.Compact(v) }
	}
	key := viewKey(n, ptr)
	r.box(ui.Column(c.Key(ptr)), n, vars).Gap(8).AlignItems(ui.Stretch).Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.textMuted).SingleLine()
		}
		// The map's shape follows its width, which is known from the last
		// frame: the first frame leaves the room and asks for another.
		body := ui.Box(c.Key("map")).Clip()
		bw := body.Bounds().W
		if bw <= 0 {
			// The first frame: the pane's width from the view, which the
			// paint corrects when it is off.
			view, _ := r.vars["view"].(map[string]any)
			w, _ := view["width"].(float64)
			bw = max(float32(w)*tmlCellW-28, 120)
		}
		cols := min(max(int(bw/mapPitch), 1), 600)
		_, rows := mp.Fit(cols, cols, plot.Blocks)
		grid := r.v.mapGrid(key, fmt.Sprint(cols, mp.Log, codes), func() *plot.Grid { return mp.Draw(cols, rows, plot.Blocks) })
		body.Height(float32(rows) * 2 * mapPitch).Draw(func(p *ui.Painter, b ui.Rect) {
			// The width changed since the last frame: draw again at it, once
			// for each width, so a width that will not settle cannot loop.
			if w := int(b.W / mapPitch); w != cols && cols < 600 && r.v.mapAsk(key, w) {
				p.After(0)
			}
			x0 := b.X + (b.W-float32(grid.W)*mapPitch)/2
			dot := func(x, y int, half int, role plot.Role) {
				if role == plot.None {
					return
				}
				p.Fill(ui.Rect{X: x0 + float32(x)*mapPitch, Y: b.Y + float32(y*2+half)*mapPitch, W: mapPitch - 0.6, H: mapPitch - 0.6}, r.chartColor(role, pal.divider), 0.8)
			}
			for y := range grid.H {
				for x := range grid.W {
					cell := grid.At(x, y)
					switch cell.Text {
					case "▀":
						dot(x, y, 0, cell.Role)
						dot(x, y, 1, cell.Bg)
					case "▄":
						dot(x, y, 1, cell.Role)
					}
				}
			}
		})
		if items := mp.Legend(plot.Blocks); len(items) > 0 {
			ui.Row(c).Gap(14).Justify(ui.Center).Children(func() {
				r.chartLegend(items)
			})
		}
	})
}

// mapGrid is the drawn map of an element, drawn again only when its width
// or values change: the world is a few hundred thousand cells to walk.
func (v *tmlView) mapGrid(key, sig string, draw func() *plot.Grid) *plot.Grid {
	if m, ok := v.maps[key]; ok && m.sig == sig {
		return m.grid
	}
	if v.maps == nil {
		v.maps = map[string]tmlMap{}
	}
	g := draw()
	v.maps[key] = tmlMap{sig: sig, grid: g, asked: v.maps[key].asked}
	return g
}

// mapAsk reports whether the map has not yet asked for a frame at width w,
// and notes that it has.
func (v *tmlView) mapAsk(key string, w int) bool {
	m := v.maps[key]
	if m.asked == w {
		return false
	}
	m.asked = w
	v.maps[key] = m
	return true
}

// tmlMap is a drawn map and what it was drawn from.
type tmlMap struct {
	sig  string
	grid *plot.Grid
	// asked is the width the map last asked for a frame at.
	asked int
}
