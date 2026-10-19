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
	height int // the rows the chart wants, without the title
	width  int // the cells it wants, 0 for the whole width
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
		w.bins = int(s.num(n, "bins", vars, 0))
		w.height = 5
	case "bar":
		rows := s.data(n, "data", vars)
		w.values = numbers(rows, n.AttrString("y"))
		w.labels = labels(rows, n.AttrString("x"))
		w.horiz = n.AttrString("dir") == "h"
		w.height = 4
		if w.horiz {
			w.height = len(w.values)
		}
	case "line", "area":
		rows := s.data(n, "data", vars)
		fields := strings.Split(n.AttrString("y"), ",")
		for i, f := range fields {
			sr := plot.Series{Name: strings.TrimSpace(f), Values: numbers(rows, strings.TrimSpace(f))}
			if i == 0 && validRole(w.role) && w.role != "" {
				sr.Role = plot.Role(w.role)
			}
			w.series = append(w.series, sr)
		}
		w.axis = s.flag(n, "axis", vars, false)
		w.height = 6
	default:
		// heatmap and pie come with the next chart wave.
		w.height = 1
	}
	return w
}

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
		return min(maxW, max(w.width, width(w.title))), h
	case "heatmap", "pie":
		return min(maxW, len(w.kind)+12), h
	}
	return maxW, h
}

func (w *chartWidget) paint(s *Surface, _ *box, c *canvas, r Rect) {
	if w.title != "" {
		c.drawLine(r.Min.X, r.Min.Y, r.Dx(), plainLine(w.title, style{fg: "dim", attrs: attrBold}), alignLeft)
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
		bar := plot.Bar{Values: w.values, Labels: w.labels, Role: role, Horizontal: w.horiz, Guide: s.pal.grounded()}
		g = bar.Draw(r.Dx(), r.Dy(), s.mode)
	case "line", "area":
		g = plot.Line{Series: w.series, Fill: w.kind == "area", Axis: w.axis}.Draw(r.Dx(), r.Dy(), s.mode)
	default:
		c.drawLine(r.Min.X, r.Min.Y, r.Dx(), plainLine("<"+w.kind+">", style{fg: "mute"}), alignLeft)
		return
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
			st := style{fg: string(cell.Role)}
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
