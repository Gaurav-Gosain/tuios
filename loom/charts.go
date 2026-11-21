package loom

import (
	"math"
	"slices"
	"strings"

	"tuios.dev/plot"
	"tuios.dev/tml"
)

type chartWidget struct {
	kind  string
	title string
	role  string
	label string

	values []float64
	labels []string
	series []plot.Series
	value  float64
	top    float64
	bins   int
	axis   bool
	horiz  bool
	donut  bool
	roles  []plot.Role
	keys   []plot.LegendItem
	grid   [][]float64
	rowsL  []string
	colsL  []string
	codes  map[string]float64
	logv   bool
	unit   string
	height int
	width  int
}

func (s *Surface) buildChart(b *box, vars map[string]any) widget {
	n := b.src
	w := &chartWidget{kind: n.Type, title: s.str(n, "title", vars), role: s.roleOr(n, vars, "")}
	switch n.Type {
	case "gauge":
		w.value = s.num(n, "value", vars, math.NaN())
		w.top = s.num(n, "max", vars, 100)
		w.label = s.str(n, "label", vars)
		w.height = 1
	case "progress":
		w.value = s.num(n, "value", vars, math.NaN())
		w.top = 1
		w.label = s.str(n, "label", vars)
		w.height = 1
	case "sparkline":
		w.values = ChartValues(s.data(n, "values", vars), "")
		w.height, w.width = 1, len(w.values)
	case "histogram":
		w.values = ChartValues(s.data(n, "values", vars), "")
		w.bins = s.count(n, "bins", vars, 0, 0, 200)
		w.height = 5
	case "bar":
		rows := s.data(n, "data", vars)
		w.values = ChartValues(rows, n.AttrString("y"))
		w.labels = ChartLabels(rows, n.AttrString("x"))
		w.horiz = n.AttrString("dir") == "h"
		w.roles, w.keys = ChartRowRoles(rows)
		w.height = 6
		if w.horiz {
			w.height = len(w.values)
		}
	case "line", "area":
		rows := s.data(n, "data", vars)
		fields := strings.Split(n.AttrString("y"), ",")
		if len(fields) > maxSeries {
			fields = fields[:maxSeries]
		}
		for i, f := range fields {
			sr := plot.Series{Name: strings.TrimSpace(f), Values: ChartValues(rows, strings.TrimSpace(f))}
			if i == 0 && validRole(w.role) && w.role != "" {
				sr.Role = plot.Role(w.role)
			}
			w.series = append(w.series, sr)
		}
		w.labels = ChartLabels(rows, n.AttrString("x"))
		w.axis = s.flag(n, "axis", vars, false)
		w.height = 6
	case "heatmap":
		w.grid, w.rowsL, w.colsL = ChartGrid(s.data(n, "data", vars), n.AttrString("x"), n.AttrString("y"), n.AttrString("v"))
		w.height = len(w.rowsL) + 2
	case "map":
		rows := s.data(n, "data", vars)
		vals := ChartValues(rows, n.AttrString("y"))
		keys := ChartLabels(rows, n.AttrString("x"))
		w.codes = make(map[string]float64, len(keys))
		for i, k := range keys {
			c := strings.ToUpper(strings.TrimSpace(k))
			if len(c) != 2 || i >= len(vals) || math.IsNaN(vals[i]) {
				continue
			}
			w.codes[c] = vals[i]
		}
		w.logv = s.flag(n, "log", vars, false)
		w.unit = s.str(n, "unit", vars)
		w.height = 0
	case "pie":
		rows := s.data(n, "data", vars)
		w.values = ChartValues(rows, n.AttrString("v"))
		w.labels = ChartLabels(rows, n.AttrString("label"))
		w.donut = n.AttrString("style") == "donut"
		w.height = 7
	}
	return w
}

// ChartRowRoles reads the colour of each bar from its row: a row's role field
// names a theme role, and its series field names the legend entry for
// that role, such as {"secs": 24.1, "role": "error", "series": "failed"}.
// A row without a valid role takes the chart's.
func ChartRowRoles(rows []any) ([]plot.Role, []plot.LegendItem) {
	var roles []plot.Role
	var keys []plot.LegendItem
	seen := map[string]bool{}
	colored := false
	for _, r := range rows {
		m, _ := r.(map[string]any)
		role, _ := m["role"].(string)
		if !validRole(role) {
			role = ""
		}
		roles = append(roles, plot.Role(role))
		if role != "" {
			colored = true
		}
		name, _ := m["series"].(string)
		if name != "" && !seen[name] {
			seen[name] = true
			keys = append(keys, plot.LegendItem{Label: name, Role: plot.Role(role)})
		}
	}
	if !colored {
		return nil, nil
	}
	return roles, keys
}

// ChartGrid turns rows of {x, y, v} into a grid: one row per y and one column
// per x, in the order they first appear. A pair with no row is NaN.
func ChartGrid(rows []any, xf, yf, vf string) ([][]float64, []string, []string) {
	var xs, ys []string
	xi, yi := map[string]int{}, map[string]int{}
	type cell struct {
		x, y int
		v    float64
	}
	var cells []cell
	for _, r := range rows {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		x, y := tml.Stringify(m[xf]), tml.Stringify(m[yf])
		if _, ok := xi[x]; !ok {
			if len(xs) >= 200 {
				continue
			}
			xi[x] = len(xs)
			xs = append(xs, x)
		}
		if _, ok := yi[y]; !ok {
			if len(ys) >= 200 {
				continue
			}
			yi[y] = len(ys)
			ys = append(ys, y)
		}
		v, ok := m[vf].(float64)
		if !ok {
			v = math.NaN()
		}
		cells = append(cells, cell{xi[x], yi[y], v})
	}
	grid := make([][]float64, len(ys))
	for i := range grid {
		grid[i] = make([]float64, len(xs))
		for j := range grid[i] {
			grid[i][j] = math.NaN()
		}
	}
	for _, c := range cells {
		grid[c.y][c.x] = c.v
	}
	return grid, ys, xs
}

const maxSeries = 12

// ChartValues reads a data array as numbers: the elements themselves, or the
// field of each row. A row with no such field, and anything that is not a
// number, is missing (NaN).
func ChartValues(rows []any, field string) []float64 {
	out := make([]float64, len(rows))
	for i, r := range rows {
		v := r
		if m, ok := r.(map[string]any); ok {
			if field == "" {
				v = firstNumber(m)
			} else {
				v = m[field]
			}
		}
		f, ok := v.(float64)
		if !ok {
			f = math.NaN()
		}
		out[i] = f
	}
	return out
}

func firstNumber(m map[string]any) any {
	best := ""
	for k, v := range m {
		if _, ok := v.(float64); ok && (best == "" || k < best) {
			best = k
		}
	}
	if best == "" {
		return nil
	}
	return m[best]
}

// ChartLabels reads the field of each row as text, or nil when the field
// is empty. A host that draws charts itself reads labels the way loom does.
func ChartLabels(rows []any, field string) []string {
	if field == "" {
		return nil
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		if m, ok := r.(map[string]any); ok {
			out[i] = tml.Stringify(m[field])
		}
	}
	return out
}

func (w *chartWidget) world(s *Surface) plot.Map {
	mp := plot.Map{Values: w.codes, Log: w.logv, Aspect: s.opts.CellAspect}
	if w.unit != "" {
		unit := w.unit
		mp.Format = func(v float64) string { return unit + plot.Compact(v) }
	}
	return mp
}

func (w *chartWidget) titleRows() int {
	if w.title != "" {
		return 1
	}
	return 0
}

func (w *chartWidget) measure(s *Surface, _ *box, maxW int) (int, int) {
	h := w.height + w.titleRows()
	switch w.kind {
	case "gauge", "progress":
		return min(maxW, max(20, width(w.label)+16)), h
	case "map":
		_, rows := w.world(s).Fit(maxW, maxW, s.mode)
		return maxW, rows + w.titleRows() + 1
	case "sparkline":
		if w.title != "" && w.height == 1 {
			return min(maxW, width(w.title)+1+w.width+1+width(w.last())), 1
		}
		return min(maxW, max(w.width, width(w.title))), h
	}
	return maxW, h
}

func (w *chartWidget) last() string {
	for _, v := range slices.Backward(w.values) {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			return plot.Compact(v)
		}
	}
	return ""
}

func (w *chartWidget) legend() []plot.LegendItem {
	if (w.kind == "line" || w.kind == "area") && len(w.series) > 1 {
		return plot.Line{Series: w.series}.Legend()
	}
	if w.kind == "bar" {
		return w.keys
	}
	return nil
}

func (w *chartWidget) paint(s *Surface, _ *box, c *canvas, r Rect) {
	if w.kind == "sparkline" && w.title != "" && r.Dy() == 1 {
		x := c.text(r.Min.X, r.Min.Y, w.title, style{fg: "dim"}, r.Dx()/3) + 1
		last := w.last()
		room := r.Max.X - x
		if last != "" {
			room -= width(last) + 1
		}
		n := min(len(w.values), max(room, 0))
		g := plot.Sparkline{Values: w.values, Role: plot.Role(w.role)}.Draw(n, 1, s.mode)
		s.blit(c, g, x, r.Min.Y)
		if last != "" && room > 0 {
			c.text(x+n+1, r.Min.Y, last, style{fg: "fg", attrs: attrBold}, -1)
		}
		return
	}
	var world plot.Map
	mapRows := 0
	if w.kind == "map" {
		world = w.world(s)
		_, mapRows = world.Fit(r.Dx(), max(r.Dy()-w.titleRows()-1, 1), s.mode)
		r.Min.Y += max(r.Dy()-w.titleRows()-mapRows-1, 0) / 2
	}
	if w.title != "" || w.legend() != nil {
		end := r.Min.X
		if w.title != "" {
			end = c.text(r.Min.X, r.Min.Y, truncate(w.title, r.Dx(), s.g.ellipsis), style{fg: "dim", attrs: attrBold}, -1)
		}
		if items := w.legend(); items != nil {
			lw := plot.LegendWidth(items)
			if r.Max.X-lw > end+1 {
				g := plot.NewGrid(lw, 1)
				plot.DrawLegend(g, 0, 0, items, s.mode)
				s.blit(c, g, r.Max.X-lw, r.Min.Y)
			}
		}
		r.Min.Y++
	}
	if r.Dx() <= 0 || r.Dy() <= 0 {
		return
	}
	role := plot.Role(w.role)
	var g *plot.Grid
	switch w.kind {
	case "gauge", "progress":
		x := r.Min.X
		if w.kind == "gauge" && w.label != "" {
			x = c.text(x, r.Min.Y, w.label, style{fg: "dim"}, r.Dx()/2) + 1
		}
		gg := plot.Gauge{Value: w.value, Max: w.top, Role: role}
		if gg.Role == plot.None {
			gg.Role = plot.Accent
		}
		if w.kind == "progress" {
			gg.Style = plot.GaugeBlocks
			gg.Label = w.label
		}
		g = gg.Draw(r.Max.X-x, 1, s.mode)
		r.Min.X = x
		r.Max.Y = r.Min.Y + 1
	case "sparkline":
		g = plot.Sparkline{Values: w.values, Role: role}.Draw(r.Dx(), r.Dy(), s.mode)
	case "histogram":
		g = plot.Histogram{Values: w.values, Bins: w.bins, Role: role}.Draw(r.Dx(), r.Dy(), s.mode)
	case "bar":
		axis := !w.horiz && r.Dy() >= 4 && r.Dx() >= 16
		bar := plot.Bar{Values: w.values, Labels: w.labels, Role: role, Roles: w.roles, Horizontal: w.horiz, Thin: w.horiz, Guide: s.pal.grounded(), Axis: axis}
		g = bar.Draw(r.Dx(), r.Dy(), s.mode)
	case "line", "area":
		axis := w.axis || w.kind == "area" && r.Dy() >= 4 && r.Dx() >= 16
		g = plot.Line{Series: w.series, Fill: w.kind == "area", Axis: axis, Labels: w.labels}.Draw(r.Dx(), r.Dy(), s.mode)
	case "map":
		g = world.Draw(r.Dx(), mapRows, s.mode)
		if r.Dy() > mapRows {
			lg := world.LegendGrid(r.Dx(), s.mode)
			s.blit(c, lg, r.Min.X+(r.Dx()-lg.W)/2, r.Min.Y+mapRows)
		}
	case "heatmap":
		g = plot.Heatmap{Values: w.grid, RowLabels: w.rowsL, ColLabels: w.colsL}.Draw(r.Dx(), r.Dy(), s.mode)
	case "pie":
		g = plot.Pie{Values: w.values, Labels: w.labels, Donut: w.donut, Aspect: s.opts.CellAspect}.Draw(r.Dx(), r.Dy(), s.mode)
	}
	s.blit(c, g, r.Min.X, r.Min.Y)
}

func (s *Surface) blit(c *canvas, g *plot.Grid, x0, y0 int) {
	for y := 0; y < g.H; y++ {
		for x := 0; x < g.W; x++ {
			cell := g.At(x, y)
			if cell.Width == 0 || cell.Text == "" {
				continue
			}
			st := style{fg: string(cell.Role), bg: string(cell.Bg)}
			if st.fg == "" {
				st.fg = "fg"
			}
			if cell.Bold {
				st = st.with(attrBold)
			}
			c.put(x0+x, y0+y, cell.Text, int(cell.Width), st)
		}
	}
}
