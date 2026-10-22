package text

import (
	"math"
	"strconv"
	"strings"

	"tuios.dev/plot"
	"tuios.dev/sketch"
	"tuios.dev/sketch/internal/types"
)

// A radar chart is drawn in braille dots: a spoke for each axis from twelve
// o'clock clockwise, as pele draws them, the graticule rings, and each curve
// as a closed polygon in its series colour, with the axis labels outside
// the rim and a legend under the chart.
//
// Without colour the curves cannot be told apart, and in 40 columns or fewer
// the chart is too small to read, so it is drawn as a table instead: each
// axis is a heading, and under it each curve is a bar as long as its value
// from the chart's minimum to its maximum, with the value after it.

func number(v float64) string {
	if math.Abs(v) >= 1e21 || math.IsNaN(v) || math.IsInf(v, 0) {
		return strconv.FormatFloat(v, 'g', -1, 64)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// radarMaxAxes is the most axes the polar drawing has room to label.
const radarMaxAxes = 16

func drawRadar(l *sketch.Layout, opt Options) (*canvas, error) {
	r := l.Radar
	if r == nil {
		return nil, ErrUnsupported
	}
	if len(r.Axes)*(len(r.Curves)+1) > 100_000 {
		return nil, ErrTooLarge
	}
	if opt.Color && (opt.Cols == 0 || opt.Cols > 40) && len(r.Axes) >= 3 && len(r.Axes) <= radarMaxAxes && r.Max > r.Min {
		if c, err := radarPolar(r, opt); err != ErrTooWide {
			return c, err
		}
	}
	return radarTable(r, opt)
}

// radarFraction is how far v is from the centre to the rim, from 0 to 1. A
// missing value is at the centre.
func radarFraction(r *types.GeoRadar, v float64) float64 {
	if math.IsNaN(v) || !(r.Max > r.Min) {
		return 0
	}
	return (math.Min(math.Max(v, r.Min), r.Max) - r.Min) / (r.Max - r.Min)
}

// radarPolar draws the chart with tuios.dev/plot's radar, in braille dots,
// with the legend under it.
func radarPolar(r *types.GeoRadar, opt Options) (*canvas, error) {
	labelW := 0
	for _, a := range r.Axes {
		labelW = max(labelW, min(width(a), 20))
	}
	// The chart is 15 rows tall, 17 when there are columns to spare, and
	// down to 9 when they are few. Its width in cells is what makes it
	// round: a cell is pieAspect times as tall as it is wide.
	rows := 15
	if opt.Cols == 0 || opt.Cols >= 110 {
		rows = 17
	}
	total := func(rows int) int {
		rho := (float64(rows)/2 - 0.25) * pieAspect
		return labelW + 2 + 2*int(math.Ceil(rho)) + 1 + 2 + labelW
	}
	if opt.Cols > 0 {
		for rows > 9 && total(rows) > opt.Cols {
			rows -= 2
		}
		if total(rows) > opt.Cols {
			return nil, ErrTooWide
		}
	}
	w := total(rows)
	series := make([]plot.Series, len(r.Curves))
	for k, curve := range r.Curves {
		series[k] = plot.Series{Name: safeString(curve.Label), Values: curve.Values, Role: plot.SeriesRole(k)}
	}
	ticks := r.Ticks
	if ticks <= 0 {
		ticks = 1
	}
	axes := make([]string, len(r.Axes))
	for i, a := range r.Axes {
		axes[i] = safeString(a)
	}
	g := plot.Radar{Axes: axes, Series: series, Min: r.Min, Max: r.Max, Rings: ticks, Polygon: r.Polygon, Aspect: pieAspect}.Draw(w, rows+2, plot.Braille)
	legend := radarLegend(r, w)
	h := g.H + 1 + len(legend)
	c, err := newCanvas(w, h)
	if err != nil {
		return nil, err
	}
	c.paste(g, 0, 0)
	for i, line := range legend {
		for _, it := range line {
			y := h - len(legend) + i
			if it.role == roleNone {
				c.text(it.x, y, it.label, it.labelRole)
				continue
			}
			c.text(it.x, y, "━━", it.role)
			c.text(it.x+3, y, it.label, it.labelRole)
		}
	}
	return withTitle(c, r.Title, opt.Cols)
}

// cut shortens s to at most w cells, ending in … when it had to.
func cut(s string, w int) string {
	return fit(s, w)
}

func value(vals []float64, i int) float64 {
	if i < len(vals) {
		return vals[i]
	}
	return math.NaN()
}

type radarItem struct {
	x               int
	role, labelRole uint8
	label           string
}

// radarLegend lays out the legend in lines of at most w cells: a mark in
// each curve's colour and its label, then the range from centre to rim.
func radarLegend(r *types.GeoRadar, w int) [][]radarItem {
	var out [][]radarItem
	var line []radarItem
	x := 0
	add := func(it radarItem, iw int) {
		if x > 0 && x+iw > w {
			out = append(out, line)
			line, x = nil, 0
		}
		it.x = x
		line = append(line, it)
		x += iw + 3
	}
	for k, curve := range r.Curves {
		label := fit(curve.Label, max(w-3, 1))
		add(radarItem{role: seriesRole(k), labelRole: roleText, label: label}, 3+width(label))
	}
	scale := number(r.Min) + " at the centre, " + number(r.Max) + " at the rim"
	add(radarItem{role: roleNone, labelRole: roleNote, label: scale}, width(scale))
	if len(line) > 0 {
		out = append(out, line)
	}
	return out
}

// radarTable draws the chart as a table: each axis a heading, each curve a
// line under it in its own colour, a blank line between axes.
func radarTable(r *types.GeoRadar, opt Options) (*canvas, error) {
	labelW, valueW := 0, 0
	for _, curve := range r.Curves {
		labelW = max(labelW, width(curve.Label))
		for _, v := range curve.Values {
			valueW = max(valueW, width(number(v)))
		}
	}
	axisW := 0
	for _, axis := range r.Axes {
		axisW = max(axisW, width(axis))
	}
	barW := 30
	if opt.Cols > 0 {
		// Two cells of indent, the label, the bar and the value, a space between each.
		labelW = min(labelW, max(8, opt.Cols/3))
		barW = min(barW, opt.Cols-2-labelW-1-1-valueW)
		if barW < 4 {
			return nil, ErrTooWide
		}
		axisW = min(axisW, opt.Cols)
	}
	scale := "from " + number(r.Min) + " to " + number(r.Max)
	w := max(2+labelW+1+barW+1+valueW, axisW, widest(r.Title), width(scale))
	if opt.Cols > 0 && w > opt.Cols {
		return nil, ErrTooWide
	}
	h := len(r.Title) + 1 + len(r.Axes)*(len(r.Curves)+2)
	c, err := newCanvas(w, h)
	if err != nil {
		return nil, err
	}
	y := 0
	for _, line := range r.Title {
		c.text((w-width(line))/2, y, line, roleTitle)
		y++
	}
	c.text(0, y, scale, roleNote)
	y++
	for i, axis := range r.Axes {
		y++
		c.text(0, y, fit(axis, w), roleHead)
		y++
		for k, curve := range r.Curves {
			v := value(curve.Values, i)
			role := seriesRole(k)
			c.text(2, y, fit(curve.Label, labelW), role)
			c.text(2+labelW+1, y, gaugeLine(radarFraction(r, v), barW), role)
			num := number(v)
			c.text(2+labelW+1+barW+1+valueW-width(num), y, num, roleNote)
			y++
		}
	}
	return c, nil
}

// gaugeLine is a line of n cells, heavy to the fraction f and dotted after:
// lines on rows next to each other stay apart, as blocks would not.
func gaugeLine(f float64, n int) string {
	if math.IsNaN(f) || f < 0 {
		f = 0
	}
	f = math.Min(f, 1)
	halves := int(math.Round(f * float64(n) * 2))
	var b strings.Builder
	b.WriteString(strings.Repeat("━", halves/2))
	used := halves / 2
	if halves%2 == 1 {
		b.WriteRune('╸')
		used++
	}
	b.WriteString(strings.Repeat("·", n-used))
	return b.String()
}
