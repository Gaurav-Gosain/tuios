// Command tplot draws a chart in the terminal from CSV, TSV or JSON data.
//
//	go install tuios.dev/plot/cmd/tplot@latest
//	printf 'session,6.2\napp,4.8\nvt,3.1\n' | tplot
//
// It reads standard input or a file, picks a chart for the shape of the data
// unless --kind names one, and writes the chart as text with ANSI colours.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"runtime/debug"
	"strings"

	"golang.org/x/term"
	"tuios.dev/plot"
)

const usage = `Usage: tplot [flags] [FILE]

Draw a chart from CSV, TSV or JSON data. FILE is a data file, or - for
standard input. Without FILE, tplot reads standard input.

Data:
  CSV or TSV    A row per point. A text column gives the labels. Each number
                column is a series. A first row of names is a header.
  numbers       One number per line, or numbers with spaces between them.
  JSON          [1, 2, 3], {"a": 1, "b": 2}, {"rx": [1, 2], "tx": [3, 4]},
                [[1, 2], [3, 4]], or [{"name": "a", "value": 1}, ...].

Kinds:
  bar       horizontal bars, a row per label (the default for a few labels)
  column    vertical bars with an axis
  line      a line per series (the default for many points or many series)
  area      a line chart filled below the line
  spark     a sparkline, one row high unless you give --height
  hist      a histogram of the values of the first series
  heatmap   rows of the labels, columns of the series
  pie       shares of a whole, with a legend
  donut     a pie with a hole
  gauge     the last value against --max
  map       a world map: labels are country codes, such as DE or jp

Flags:
`

const examples = `
Examples:
  printf 'session,6.2\napp,4.8\nvt,3.1\n' | tplot
  seq 1 50 | awk '{print sin($1/5)}' | tplot --kind line
  tplot --kind pie shares.csv
  echo '{"US": 410, "DE": 120, "SG": 14}' | tplot --kind map --log
  curl -s api.example.com/stats | tplot --kind column --title Requests
`

type options struct {
	kind, mode, color, input, title, columns string
	width, height                            int
	min, max                                 float64
	log, noHeader                            bool
}

// version is set by the release build. go install leaves it empty, and the
// module version comes from the build info.
var version string

func versionString() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "dev"
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "tplot:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout *os.File, stderr io.Writer) error {
	var o options
	fs := flag.NewFlagSet("tplot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
		fmt.Fprint(stderr, examples)
	}
	fs.StringVar(&o.kind, "kind", "", "Chart `kind`: bar, column, line, area, spark, hist, heatmap, pie, donut, gauge or map")
	fs.StringVar(&o.kind, "k", "", "Short for --kind")
	fs.IntVar(&o.width, "width", 0, "Width in cells, 0 for the terminal width")
	fs.IntVar(&o.width, "w", 0, "Short for --width")
	fs.IntVar(&o.height, "height", 0, "Height in cells, 0 to fit the data")
	fs.StringVar(&o.mode, "mode", "braille", "Glyphs: braille, blocks or ascii")
	fs.StringVar(&o.color, "color", "auto", "Colour: auto, always or never")
	fs.StringVar(&o.input, "input", "", "Input `format`: csv, tsv, json or numbers. Empty means a guess from the data")
	fs.StringVar(&o.title, "title", "", "A title above the chart")
	fs.StringVar(&o.columns, "columns", "", "The number columns to draw, by name or by number from 1, such as rx,tx")
	fs.Float64Var(&o.min, "min", 0, "The bottom of the range. --min 0 --max 0 means automatic")
	fs.Float64Var(&o.max, "max", 0, "The top of the range, and the full value of a gauge (100 when 0)")
	fs.BoolVar(&o.log, "log", false, "With --kind map, shade by the logarithm of the values")
	fs.BoolVar(&o.noHeader, "no-header", false, "The first row is data, not names")
	showVersion := fs.Bool("version", false, "Print the version and exit")
	// Flags may come before or after FILE.
	var files []string
	for {
		if err := fs.Parse(args); err != nil {
			return err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		files = append(files, args[0])
		args = args[1:]
	}
	if *showVersion {
		fmt.Fprintln(stdout, "tplot", versionString())
		return nil
	}
	if len(files) > 1 {
		return fmt.Errorf("give one file, not %d", len(files))
	}

	switch strings.ToLower(o.kind) {
	case "", "bar", "bars", "column", "columns", "line", "area", "spark", "sparkline", "hist", "histogram",
		"heatmap", "pie", "donut", "gauge", "map":
	default:
		return fmt.Errorf("unknown kind %q: use bar, column, line, area, spark, hist, heatmap, pie, donut, gauge or map", o.kind)
	}
	mode, err := parseMode(o.mode)
	if err != nil {
		return err
	}
	color, err := useColor(o.color, stdout)
	if err != nil {
		return err
	}
	modeSet := false
	fs.Visit(func(f *flag.Flag) { modeSet = modeSet || f.Name == "mode" })

	r := stdin
	name := "-"
	if len(files) == 1 && files[0] != "-" {
		name = files[0]
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		r = f
	} else if f, ok := stdin.(*os.File); ok && len(files) == 0 && term.IsTerminal(int(f.Fd())) {
		fs.Usage()
		return errors.New("give a file, or pipe data to standard input")
	}
	data, err := io.ReadAll(io.LimitReader(r, maxInput+1))
	if err != nil {
		return err
	}
	if len(data) > maxInput {
		return fmt.Errorf("the input is larger than %d MiB", maxInput>>20)
	}
	t, err := readTable(data, o.input, name, o.noHeader)
	if err != nil {
		return err
	}
	if o.columns != "" {
		if err := t.pick(o.columns); err != nil {
			return err
		}
	}
	if len(t.series) == 0 {
		return errors.New("the data has no column of numbers")
	}

	cols, rows := termSize(stdout)
	width := o.width
	if width <= 0 {
		width = cols
	}
	if !color && !modeSet && needsColor(t, o.kind) {
		// Without colour, the shades, slices and series of these charts look
		// the same. ASCII gives each its own mark.
		mode = plot.ASCII
	}
	g, err := draw(t, o, mode, width, rows)
	if err != nil {
		return err
	}
	if o.title != "" {
		fmt.Fprintln(stdout, title(o.title, color))
	}
	_, err = io.WriteString(stdout, render(g, color))
	return err
}

// maxInput bounds what tplot reads: far more data than a terminal can show.
const maxInput = 64 << 20

func parseMode(s string) (plot.Mode, error) {
	switch strings.ToLower(s) {
	case "braille", "":
		return plot.Braille, nil
	case "blocks":
		return plot.Blocks, nil
	case "ascii":
		return plot.ASCII, nil
	}
	return 0, fmt.Errorf("unknown mode %q: use braille, blocks or ascii", s)
}

// useColor says whether to write colours: always, never, or for a terminal
// when NO_COLOR is not set and TERM is not dumb.
func useColor(s string, out *os.File) (bool, error) {
	switch strings.ToLower(s) {
	case "always":
		return true, nil
	case "never":
		return false, nil
	case "auto", "":
		return term.IsTerminal(int(out.Fd())) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb", nil
	}
	return false, fmt.Errorf("unknown colour setting %q: use auto, always or never", s)
}

// termSize is the terminal's size, or 80 by 24 when the output is not a
// terminal.
func termSize(out *os.File) (cols, rows int) {
	if w, h, err := term.GetSize(int(out.Fd())); err == nil && w > 0 && h > 0 {
		return w, h
	}
	return 80, 24
}

// draw picks the chart and draws it at a height that fits the data.
func draw(t *table, o options, m plot.Mode, w, termRows int) (*plot.Grid, error) {
	kind := strings.ToLower(o.kind)
	if kind == "" {
		kind = t.guess()
	}
	first := t.series[0]
	fit := func(want int) int {
		if o.height > 0 {
			return o.height
		}
		return max(3, min(want, termRows-2))
	}
	switch kind {
	case "bar", "bars":
		h := fit(len(first.values))
		return plot.Bar{Values: first.values, Labels: t.labels, Horizontal: true, Min: o.min, Max: o.max}.Draw(w, h, m), nil
	case "column", "columns":
		return plot.Bar{Values: first.values, Labels: t.labels, Axis: true, Min: o.min, Max: o.max}.Draw(w, fit(15), m), nil
	case "line", "area":
		l := plot.Line{Fill: kind == "area", Axis: true, Labels: t.labels, Min: o.min, Max: o.max}
		for _, s := range t.series {
			l.Series = append(l.Series, plot.Series{Name: s.name, Values: s.values})
		}
		legend := l.Legend()
		if len(l.Series) < 2 {
			legend = nil
		}
		h := fit(15)
		if len(legend) == 0 {
			return l.Draw(w, h, m), nil
		}
		// The legend takes the first row.
		return withLegend(l.Draw(w, max(h-1, 2), m), legend, m), nil
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
		n := len(first.values)
		for i := range n {
			row := make([]float64, len(t.series))
			for k, s := range t.series {
				row[k] = s.values[i]
			}
			hm.Values = append(hm.Values, row)
		}
		for _, s := range t.series {
			hm.ColLabels = append(hm.ColLabels, s.name)
		}
		// A row per value row, the column labels and the scale.
		return hm.Draw(w, fit(n+3), m), nil
	case "pie", "donut":
		p := plot.Pie{Values: first.values, Labels: t.labels, Donut: kind == "donut"}
		return p.Draw(w, fit(12), m), nil
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
		// The map keeps its shape: about 2.2 cells of width per row.
		h := fit(int(math.Round(float64(w) * 144 / 360 / 2.2 * 2)))
		return withLegend(mp.Draw(w, max(h-1, 2), m), mp.Legend(m), m), nil
	}
	return nil, fmt.Errorf("unknown kind %q: use bar, column, line, area, spark, hist, heatmap, pie, donut, gauge or map", o.kind)
}

// needsColor says whether a chart tells its parts apart only by colour in
// braille and block glyphs.
func needsColor(t *table, kind string) bool {
	kind = strings.ToLower(kind)
	if kind == "" {
		kind = t.guess()
	}
	switch kind {
	case "heatmap", "pie", "donut", "map":
		return true
	case "line", "area":
		return len(t.series) > 1
	}
	return false
}

// withLegend puts a legend row above g.
func withLegend(g *plot.Grid, items []plot.LegendItem, m plot.Mode) *plot.Grid {
	out := plot.NewGrid(g.W, g.H+1)
	plot.DrawLegend(out, 0, 0, items, m)
	copy(out.Cells[g.W:], g.Cells)
	return out
}
