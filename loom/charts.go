package loom

import (
	"math"
	"strings"

	"tuios.dev/plot"
	"tuios.dev/tml"
)

// chartWidget draws a chart component through tuios.dev/plot: bar, line,
// area, sparkline, gauge, progress and histogram. A title takes the first
// row.
type chartWidget struct {
	kind  string
	title string
	role  string
	label string // gauge: the label at the left

	values []float64
	labels []string
	series []plot.Series
	value  float64
	top    float64
	bins   int
	axis   bool
	horiz  bool
	donut  bool
	roles  []plot.Role // bar: a role per row, from the row's role field
	keys   []plot.LegendItem
	grid   [][]float64 // heatmap: rows of columns
	rowsL  []string    // heatmap row labels
	colsL  []string    // heatmap column labels
	height int         // the rows the chart wants, without the title
	width  int         // the cells it wants, 0 for the whole width
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
		w.values = numbers(s.data(n, "values", vars), "")
		w.height, w.width = 1, len(w.values)
	case "histogram":
		w.values = numbers(s.data(n, "values", vars), "")
		w.bins = s.count(n, "bins", vars, 0, 0, 200)
		w.height = 5
	case "bar":
		rows := s.data(n, "data", vars)
		w.values = numbers(rows, n.AttrString("y"))
		w.labels = labels(rows, n.AttrString("x"))
		w.horiz = n.AttrString("dir") == "h"
		w.roles, w.keys = rowRoles(rows)
		w.height = 6
		if w.horiz {
			w.height = len(w.values)
		}
	case "line", "area":
		rows := s.data(n, "data", vars)
		fields := strings.Split(n.AttrString("y"), ",")
		// Each field allocates a value per row, so the list is bounded;
		// the series roles cycle after six anyway.
		if len(fields) > maxSeries {
			fields = fields[:maxSeries]
		}
		for i, f := range fields {
			sr := plot.Series{Name: strings.TrimSpace(f), Values: numbers(rows, strings.TrimSpace(f))}
			if i == 0 && validRole(w.role) && w.role != "" {
				sr.Role = plot.Role(w.role)
			}
			w.series = append(w.series, sr)
		}
		w.labels = labels(rows, n.AttrString("x"))
		w.axis = s.flag(n, "axis", vars, false)
		w.height = 6
	case "heatmap":
		w.grid, w.rowsL, w.colsL = pivot(s.data(n, "data", vars), n.AttrString("x"), n.AttrString("y"), n.AttrString("v"))
		w.height = len(w.rowsL) + 2
	case "pie":
		rows := s.data(n, "data", vars)
		w.values = numbers(rows, n.AttrString("v"))
		w.labels = labels(rows, n.AttrString("label"))
		w.donut = n.AttrString("style") == "donut"
		w.height = 7
	}
	return w
}

// rowRoles reads the colour of each bar from its row: a row's role field
// names a theme role, and its series field names the legend entry for
// that role, such as {"secs": 24.1, "role": "error", "series": "failed"}.
// A row without a valid role takes the chart's.
func rowRoles(rows []any) ([]plot.Role, []plot.LegendItem) {
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

// pivot turns rows of {x, y, v} into a grid: one row per y and one column
// per x, in the order they first appear. A pair with no row is NaN.
func pivot(rows []any, xf, yf, vf string) ([][]float64, []string, []string) {
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

// maxSeries bounds the y fields of a line or an area.
const maxSeries = 12

// numbers reads a data array as numbers: the elements themselves, or the
// field of each row. A row with no such field, and anything that is not a
// number, is missing (NaN).
func numbers(rows []any, field string) []float64 {
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

// firstNumber returns a number field of a row, by name so the choice does
// not depend on map order.
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

func labels(rows []any, field string) []string {
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
	case "sparkline":
		if w.title != "" && w.height == 1 {
			// One row: the title, the trend, the last value.
			return min(maxW, width(w.title)+1+w.width+1+width(w.last())), 1
		}
		return min(maxW, max(w.width, width(w.title))), h
	}
	return maxW, h
}

// last is the last value of a sparkline as text.
func (w *chartWidget) last() string {
	for i := len(w.values) - 1; i >= 0; i-- {
		if v := w.values[i]; !math.IsNaN(v) && !math.IsInf(v, 0) {
			return plot.Compact(v)
		}
	}
	return ""
}

// legend is what the title row shows at the right: the series of a line
// or an area with more than one.
func (w *chartWidget) legend() []plot.LegendItem {
	if (w.kind == "line" || w.kind == "area") && len(w.series) > 1 {
		return plot.Line{Series: w.series}.Legend()
	}
	if w.kind == "bar" {
		// The legend names the chart's own role as well, when some rows
		// take it.
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
			// The last value sits right after the trend, where the eye ends.
			c.text(x+n+1, r.Min.Y, last, style{fg: "fg", attrs: attrBold}, -1)
		}
		return
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
		// Columns get an axis when they have the room for one.
		axis := !w.horiz && r.Dy() >= 4 && r.Dx() >= 16
		bar := plot.Bar{Values: w.values, Labels: w.labels, Role: role, Roles: w.roles, Horizontal: w.horiz, Guide: s.pal.grounded(), Axis: axis}
		g = bar.Draw(r.Dx(), r.Dy(), s.mode)
	case "line", "area":
		// An area has no axis attribute; it gets one when it has the room,
		// as columns do.
		axis := w.axis || w.kind == "area" && r.Dy() >= 4 && r.Dx() >= 16
		g = plot.Line{Series: w.series, Fill: w.kind == "area", Axis: axis, Labels: w.labels}.Draw(r.Dx(), r.Dy(), s.mode)
	case "heatmap":
		g = plot.Heatmap{Values: w.grid, RowLabels: w.rowsL, ColLabels: w.colsL}.Draw(r.Dx(), r.Dy(), s.mode)
	case "pie":
		g = plot.Pie{Values: w.values, Labels: w.labels, Donut: w.donut, Aspect: s.opts.CellAspect}.Draw(r.Dx(), r.Dy(), s.mode)
	}
	s.blit(c, g, r.Min.X, r.Min.Y)
}

// blit copies a plot grid onto the canvas, its roles resolved by the
// palette.
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
