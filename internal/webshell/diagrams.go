package webshell

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"tuios.dev/plot"
	"tuios.dev/sketch"
	sketchtext "tuios.dev/sketch/text"
)

// Diagrams and charts in the demo, drawn by the real libraries:
// tuios mermaid and sketch draw Mermaid as text with tuios.dev/sketch, and
// tplot draws CSV data with tuios.dev/plot. A real terminal that shows
// pictures gets a picture from tuios mermaid. The browser draws text, at the
// pane's width, and draws it again when the pane changes width.

// cmdMermaid is tuios mermaid.
func cmdMermaid(s *shell, in []string) int {
	return drawDiagrams(s, "tuios mermaid", in,
		[]string{"cols", "rows", "diagram", "theme", "format", "f", "out", "o", "background", "scale"},
		[]string{"fit", "watch", "w", "split", "demo", "loop"})
}

// cmdSketch is the sketch command of tuios.dev/sketch.
func cmdSketch(s *shell, in []string, _ string) int {
	return drawDiagrams(s, "sketch", in[1:],
		[]string{"cols", "diagram", "theme", "f", "o", "color", "scale"},
		[]string{"themes"})
}

func drawDiagrams(s *shell, cmd string, in []string, valued, bools []string) int {
	a, err := parseArgs(in, valued, bools)
	if err != nil {
		return s.fail(cmd + ": " + err.Error())
	}
	for _, live := range []string{"watch", "w", "demo", "split", "loop"} {
		if a.has(live) {
			s.t.Print(yellow + cmd + " --" + live + reset + " needs a real machine. Here it draws the file once.\r\n")
			break
		}
	}
	if a.has("themes") {
		for _, th := range sketch.Themes() {
			s.t.Print(th.Name + "\r\n")
		}
		return 0
	}
	format := strings.ToLower(a.flags["format"] + a.flags["f"])
	if format != "" && format != "text" || a.flags["out"]+a.flags["o"] != "" {
		return s.fail(cmd + ": the browser draws text only. Drop the format and out flags.")
	}
	name, data, ok := s.readSource(cmd, a)
	if !ok {
		return 1
	}
	sources := mermaidSources(name, string(data))
	if len(sources) == 0 {
		return s.fail(cmd + ": " + name + " has no Mermaid diagram")
	}
	if n := a.int("diagram"); n > 0 {
		if n > len(sources) {
			return s.fail(fmt.Sprintf("%s: %s has %d diagrams, not %d", cmd, name, len(sources), n))
		}
		sources = sources[n-1 : n]
	}
	diagrams := make([]*sketch.Diagram, len(sources))
	failed := 0
	for i, src := range sources {
		d, err := sketch.Parse(src, sketch.ParseOptions{})
		if err != nil {
			failed++
			s.fail(fmt.Sprintf("diagram %d: %s", i+1, err))
			continue
		}
		diagrams[i] = d
	}
	width := a.int("cols")
	color := a.flags["color"] != "never"
	s.t.Lay(func(cols int) []string {
		w := cols
		if width > 0 {
			w = width
		}
		var out []string
		for i, d := range diagrams {
			if d == nil {
				continue
			}
			if len(out) > 0 {
				out = append(out, "")
			}
			lines, err := sketchtext.Draw(d, sketchtext.Options{Cols: w, Color: color})
			if errors.Is(err, sketchtext.ErrTooWide) {
				// The real command draws it at its full width and lets the
				// terminal wrap it. Here a line too wide is cut.
				lines, err = sketchtext.Draw(d, sketchtext.Options{Color: color})
				if err == nil {
					out = append(out, wrapWords(fmt.Sprintf("%sdiagram %d: the text drawing is wider than %d columns.%s", yellow, i+1, w, reset), cols)...)
				}
			}
			switch {
			case err == nil:
				out = append(out, lines...)
			case errors.Is(err, sketchtext.ErrUnsupported), errors.Is(err, sketchtext.ErrTooLarge):
				out = append(out, wrapWords(fmt.Sprintf("%sdiagram %d: there is no text drawing for it. The source follows.%s", yellow, i+1, reset), cols)...)
				out = append(out, strings.Split(strings.TrimRight(sources[i], "\n"), "\n")...)
			default:
				out = append(out, wrapWords(fmt.Sprintf("%sdiagram %d: %s%s", red, i+1, err, reset), cols)...)
			}
		}
		return fitLines(out, cols)
	})
	if failed > 0 {
		return s.fail(fmt.Sprintf("%d of %d diagrams could not be drawn", failed, len(sources)))
	}
	return 0
}

// mermaidSources is the diagrams of a file: a Markdown file gives each of
// its mermaid code blocks, any other file is one diagram.
func mermaidSources(name, data string) []string {
	lower := strings.ToLower(name)
	if !strings.HasSuffix(lower, ".md") && !strings.HasSuffix(lower, ".markdown") {
		if strings.TrimSpace(data) == "" {
			return nil
		}
		return []string{data}
	}
	var out []string
	var cur []string
	in := false
	for line := range strings.SplitSeq(data, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case !in && (trimmed == "```mermaid" || trimmed == "~~~mermaid"):
			in, cur = true, nil
		case in && (trimmed == "```" || trimmed == "~~~"):
			in = false
			out = append(out, strings.Join(cur, "\n")+"\n")
		case in:
			cur = append(cur, line)
		}
	}
	return out
}

// plotTable is CSV data as tplot reads it: the labels of the rows, from the
// first text column, and a series per number column.
type plotTable struct {
	labels []string
	series []plotSeries
}

type plotSeries struct {
	name   string
	named  bool
	values []float64
}

// cmdTplot is the tplot command of tuios.dev/plot, for CSV, TSV and plain
// numbers.
func cmdTplot(s *shell, in []string, _ string) int {
	a, err := parseArgs(in[1:], []string{"kind", "k", "width", "w", "height", "mode", "color", "input", "title", "columns", "min", "max"},
		[]string{"log", "no-header", "version", "help", "h"})
	if err != nil {
		return s.fail("tplot: " + err.Error())
	}
	if a.has("version") {
		s.t.Print("tplot (browser demo)\r\n")
		return 0
	}
	if a.has("help") || a.has("h") || len(a.files) == 0 {
		s.t.Lay(func(cols int) []string {
			lines := wrapWords("Usage: tplot [flags] FILE", cols)
			lines = append(lines, table([][2]string{
				{"tplot sales.csv", "a chart for the shape of the data"},
				{"tplot sales.csv --kind column", "bars with an axis"},
				{"tplot sales.csv --kind line", "a line per column"},
			}, 2, green, cols)...)
			return append(lines, wrapWords(dim+"Kinds: bar, column, line, area, spark, hist, heatmap, pie, donut, gauge, map."+reset, cols)...)
		})
		return 0
	}
	name, data, ok := s.readSource("tplot", a)
	if !ok {
		return 1
	}
	kind := strings.ToLower(a.flags["kind"] + a.flags["k"])
	switch kind {
	case "", "bar", "bars", "column", "columns", "line", "area", "spark", "sparkline", "hist", "histogram",
		"heatmap", "pie", "donut", "gauge", "map":
	default:
		return s.fail("tplot: unknown kind " + strconv.Quote(kind) + ": use bar, column, line, area, spark, hist, heatmap, pie, donut, gauge or map")
	}
	mode := plot.Braille
	switch strings.ToLower(a.flags["mode"]) {
	case "", "braille":
	case "blocks":
		mode = plot.Blocks
	case "ascii":
		mode = plot.ASCII
	default:
		return s.fail("tplot: unknown mode " + strconv.Quote(a.flags["mode"]) + ": use braille, blocks or ascii")
	}
	color := a.flags["color"] != "never"
	t, err := readPlotTable(data, name, a.has("no-header"))
	if err == nil && a.flags["columns"] != "" {
		err = t.pick(a.flags["columns"])
	}
	if err == nil && len(t.series) == 0 {
		err = errors.New("the data has no column of numbers")
	}
	if err != nil {
		return s.fail("tplot: " + err.Error())
	}
	if kind == "" {
		kind = t.guess()
	}
	if !color && a.flags["mode"] == "" && t.needsColor(kind) {
		mode = plot.ASCII
	}
	o := plotOptions{kind: kind, height: a.int("height"), log: a.has("log")}
	o.min, _ = strconv.ParseFloat(a.flags["min"], 64)
	o.max, _ = strconv.ParseFloat(a.flags["max"], 64)
	width := a.int("width") + a.int("w")
	title := a.flags["title"]
	_, rows := s.t.Size()
	var drawErr error
	s.t.Lay(func(cols int) []string {
		w := cols
		if width > 0 {
			w = width
		}
		g, err := t.draw(o, mode, w, rows)
		if err != nil {
			drawErr = err
			return wrapWords(red+"tplot: "+err.Error()+reset, cols)
		}
		var out []string
		if title != "" {
			out = append(out, bold+title+reset)
		}
		out = append(out, renderGrid(g, color)...)
		return fitLines(out, cols)
	})
	if drawErr != nil {
		return 1
	}
	return 0
}

type plotOptions struct {
	kind     string
	height   int
	min, max float64
	log      bool
}

// readPlotTable reads CSV, TSV or numbers with spaces between them.
func readPlotTable(data []byte, name string, noHeader bool) (*plotTable, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("the input is empty")
	}
	first, _, _ := bytes.Cut(trimmed, []byte{'\n'})
	var records [][]string
	switch {
	case bytes.Contains(first, []byte{'\t'}), bytes.Contains(first, []byte{','}), strings.HasSuffix(strings.ToLower(name), ".csv"):
		r := csv.NewReader(bytes.NewReader(trimmed))
		if bytes.Contains(first, []byte{'\t'}) {
			r.Comma = '\t'
		}
		r.FieldsPerRecord = -1
		r.TrimLeadingSpace = true
		r.LazyQuotes = true
		for {
			rec, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("cannot read the CSV: %w", err)
			}
			if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
				continue
			}
			records = append(records, rec)
		}
	default:
		for line := range strings.SplitSeq(string(trimmed), "\n") {
			if f := strings.Fields(line); len(f) > 0 {
				records = append(records, f)
			}
		}
	}
	header := !noHeader && plotHasHeader(records)
	var names []string
	if header {
		names, records = records[0], records[1:]
	}
	if len(records) == 0 {
		return nil, errors.New("the input has no rows of data")
	}
	cols := 0
	for _, rec := range records {
		cols = max(cols, len(rec))
	}
	cell := func(rec []string, c int) string {
		if c < len(rec) {
			return rec[c]
		}
		return ""
	}
	t := &plotTable{}
	labelCol := -1
	for c := range cols {
		numeric := true
		for _, rec := range records {
			if _, ok := plotNumber(cell(rec, c)); !ok {
				numeric = false
				break
			}
		}
		if !numeric {
			if labelCol < 0 {
				labelCol = c
			}
			continue
		}
		s := plotSeries{values: make([]float64, len(records))}
		if c < len(names) {
			s.name = strings.TrimSpace(names[c])
		}
		s.named = s.name != ""
		if !s.named {
			s.name = "column " + strconv.Itoa(c+1)
		}
		for i, rec := range records {
			s.values[i], _ = plotNumber(cell(rec, c))
		}
		t.series = append(t.series, s)
	}
	if labelCol >= 0 {
		for _, rec := range records {
			t.labels = append(t.labels, strings.TrimSpace(cell(rec, labelCol)))
		}
	}
	return t, nil
}

// plotNumber parses a cell: a number, with a trailing % allowed. An empty
// cell is a missing value. ok is false for text.
func plotNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return math.NaN(), true
	}
	v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	return v, err == nil
}

// plotHasHeader says whether the first record names the columns: one of its
// cells is text where the rest of that column is numbers.
func plotHasHeader(records [][]string) bool {
	if len(records) < 2 {
		return false
	}
	for c, cell := range records[0] {
		if v, ok := plotNumber(cell); ok && !math.IsNaN(v) || strings.TrimSpace(cell) == "" {
			continue
		}
		numeric, seen := true, false
		for _, rec := range records[1:] {
			if c >= len(rec) || strings.TrimSpace(rec[c]) == "" {
				continue
			}
			seen = true
			if _, ok := plotNumber(rec[c]); !ok {
				numeric = false
				break
			}
		}
		if numeric && seen {
			return true
		}
	}
	return false
}

// pick keeps the series a list names, by name or by number from 1.
func (t *plotTable) pick(list string) error {
	var out []plotSeries
	for want := range strings.SplitSeq(list, ",") {
		want = strings.TrimSpace(want)
		found := false
		for i, s := range t.series {
			if strings.EqualFold(s.name, want) || strconv.Itoa(i+1) == want {
				out, found = append(out, s), true
				break
			}
		}
		if !found {
			var names []string
			for _, s := range t.series {
				names = append(names, s.name)
			}
			return fmt.Errorf("no number column %q. The number columns are: %s", want, strings.Join(names, ", "))
		}
	}
	t.series = out
	return nil
}

// guess picks a chart for the shape of the data.
func (t *plotTable) guess() string {
	switch {
	case len(t.series) > 1:
		return "line"
	case len(t.labels) > 0 && len(t.series[0].values) <= 40:
		return "bar"
	}
	return "line"
}

// needsColor says whether a chart tells its parts apart only by colour.
func (t *plotTable) needsColor(kind string) bool {
	switch kind {
	case "heatmap", "pie", "donut", "map":
		return true
	case "line", "area":
		return len(t.series) > 1
	}
	return false
}

// draw picks the chart and draws it at a height that fits the data, as
// tplot does.
func (t *plotTable) draw(o plotOptions, m plot.Mode, w, termRows int) (*plot.Grid, error) {
	first := t.series[0]
	fit := func(want int) int {
		if o.height > 0 {
			return o.height
		}
		return max(3, min(want, termRows-2))
	}
	switch o.kind {
	case "bar", "bars":
		return plot.Bar{Values: first.values, Labels: t.labels, Horizontal: true, Min: o.min, Max: o.max}.Draw(w, fit(len(first.values)), m), nil
	case "column", "columns":
		return plot.Bar{Values: first.values, Labels: t.labels, Axis: true, Min: o.min, Max: o.max}.Draw(w, fit(15), m), nil
	case "line", "area":
		l := plot.Line{Fill: o.kind == "area", Axis: true, Labels: t.labels, Min: o.min, Max: o.max}
		for _, s := range t.series {
			l.Series = append(l.Series, plot.Series{Name: s.name, Values: s.values})
		}
		h := fit(15)
		if len(l.Series) < 2 {
			return l.Draw(w, h, m), nil
		}
		return withLegend(l.Draw(w, max(h-1, 2), m), l.Legend(), m), nil
	case "spark", "sparkline":
		h := 1
		if o.height > 0 {
			h = o.height
		}
		return plot.Sparkline{Values: first.values, Min: o.min, Max: o.max}.Draw(w, h, m), nil
	case "hist", "histogram":
		return plot.Histogram{Values: first.values}.Draw(w, fit(15), m), nil
	case "heatmap":
		hm := plot.Heatmap{RowLabels: t.labels, Min: o.min, Max: o.max}
		for i := range first.values {
			row := make([]float64, len(t.series))
			for k, s := range t.series {
				row[k] = s.values[i]
			}
			hm.Values = append(hm.Values, row)
		}
		for _, s := range t.series {
			hm.ColLabels = append(hm.ColLabels, s.name)
		}
		return hm.Draw(w, fit(len(first.values)+3), m), nil
	case "pie", "donut":
		return plot.Pie{Values: first.values, Labels: t.labels, Donut: o.kind == "donut"}.Draw(w, fit(12), m), nil
	case "gauge":
		v := math.NaN()
		for _, x := range first.values {
			if !math.IsNaN(x) {
				v = x
			}
		}
		top := o.max
		if top == 0 {
			top = 100
		}
		label := ""
		if first.named {
			label = first.name + " " + plot.Compact(v)
		}
		return plot.Gauge{Value: v, Max: top, Label: label, Style: plot.GaugeBlocks}.Draw(w, 1, m), nil
	case "map":
		if len(t.labels) == 0 {
			return nil, errors.New("a map needs a column of country codes, such as DE or jp")
		}
		mp := plot.Map{Values: map[string]float64{}, Log: o.log, Min: o.min, Max: o.max}
		for i, code := range t.labels {
			if v := first.values[i]; !math.IsNaN(v) {
				mp.Values[code] = max(mp.Values[code], v)
			}
		}
		h := fit(int(math.Round(float64(w) * 144 / 360 / 2.2 * 2)))
		return withLegend(mp.Draw(w, max(h-1, 2), m), mp.Legend(m), m), nil
	}
	return nil, fmt.Errorf("unknown kind %q", o.kind)
}

// withLegend puts a legend row above g.
func withLegend(g *plot.Grid, items []plot.LegendItem, m plot.Mode) *plot.Grid {
	out := plot.NewGrid(g.W, g.H+1)
	plot.DrawLegend(out, 0, 0, items, m)
	copy(out.Cells[g.W:], g.Cells)
	return out
}

// plotSlots maps each role to an ANSI colour slot, as tplot does, so a
// chart takes the colours of the tuios theme.
var plotSlots = map[plot.Role]int{
	plot.Fg: 15, plot.Dim: 7, plot.Mute: 8, plot.Edge: 8, plot.Track: 8,
	plot.Accent: 5, plot.AccentBright: 13,
	plot.Success: 10, plot.Error: 9, plot.Warning: 11, plot.Info: 12,
	plot.Series1: 13, plot.Series2: 12, plot.Series3: 10, plot.Series4: 11, plot.Series5: 9, plot.Series6: 14,
	plot.Heat1: 4, plot.Heat2: 12, plot.Heat3: 6, plot.Heat4: 5, plot.Heat5: 13,
}

// plotSlot is the ANSI slot of a role, or -1. A faded bright colour is its
// normal slot from fade 2.
func plotSlot(r plot.Role) int {
	base, k := r.Faded()
	c, ok := plotSlots[base]
	if !ok {
		return -1
	}
	if k >= 2 && c >= 9 && c <= 14 {
		c -= 8
	}
	return c
}

func plotSGR(fg, bg int, bold bool) string {
	var p []string
	if bold {
		p = append(p, "1")
	}
	code := func(slot, normal, bright int) string {
		if slot >= 8 {
			return strconv.Itoa(bright + slot - 8)
		}
		return strconv.Itoa(normal + slot)
	}
	if fg >= 0 {
		p = append(p, code(fg, 30, 90))
	}
	if bg >= 0 {
		p = append(p, code(bg, 40, 100))
	}
	if len(p) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(p, ";") + "m"
}

// renderGrid writes a grid as lines, with SGR colours when color is set.
func renderGrid(g *plot.Grid, color bool) []string {
	if !color {
		return strings.Split(strings.TrimRight(g.String(), "\n"), "\n")
	}
	var out []string
	for y := 0; y < g.H; y++ {
		cur := ""
		var line strings.Builder
		for x := 0; x < g.W; x++ {
			c := g.At(x, y)
			if c.Width == 0 {
				continue
			}
			text := c.Text
			if text == "" {
				text = " "
			}
			want := ""
			if c.Text != "" || c.Bg != plot.None {
				want = plotSGR(plotSlot(c.Role), plotSlot(c.Bg), c.Bold)
			}
			if want != cur {
				if cur != "" {
					line.WriteString(reset)
				}
				line.WriteString(want)
				cur = want
			}
			line.WriteString(text)
		}
		if cur != "" {
			line.WriteString(reset)
		}
		out = append(out, strings.TrimRight(line.String(), " "))
	}
	return out
}
