package text

import (
	"math"
	"strconv"
	"strings"

	"tuios.dev/sketch"
)

// A radar chart is drawn as a table, not as a polar plot: a circle of a few cells loses the
// values. Each axis is a heading, and under it each curve is a bar as long as its value from
// the chart's minimum to its maximum, with the value after it.

// gaugeBar is a bar of n cells, filled to the fraction f.
func gaugeBar(f float64, n int) string {
	if math.IsNaN(f) || f < 0 {
		f = 0
	}
	f = math.Min(f, 1)
	parts := int(math.Round(f * float64(n) * 8))
	var b strings.Builder
	b.WriteString(strings.Repeat("█", parts/8))
	used := parts / 8
	if parts%8 > 0 {
		b.WriteRune(eighths[parts%8-1])
		used++
	}
	b.WriteString(strings.Repeat("·", n-used))
	return b.String()
}

// cut shortens s to at most w cells, ending in … when it had to.
func cut(s string, w int) string {
	if width(s) <= w {
		return s
	}
	if w < 1 {
		return ""
	}
	r := []rune(s)
	for len(r) > 0 && width(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func number(v float64) string {
	if math.Abs(v) >= 1e21 || math.IsNaN(v) || math.IsInf(v, 0) {
		return strconv.FormatFloat(v, 'g', -1, 64)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func drawRadar(l *sketch.Layout, opt Options) (*canvas, error) {
	r := l.Radar
	if r == nil {
		return nil, ErrUnsupported
	}
	if len(r.Axes)*(len(r.Curves)+1) > 100_000 {
		return nil, ErrTooLarge
	}
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
	h := len(r.Title) + 1 + len(r.Axes)*(len(r.Curves)+1)
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
		c.text(0, y, cut(axis, w), roleHead)
		y++
		for _, curve := range r.Curves {
			v := math.NaN()
			if i < len(curve.Values) {
				v = curve.Values[i]
			}
			f := (math.Min(math.Max(v, r.Min), r.Max) - r.Min) / (r.Max - r.Min)
			c.text(2, y, cut(curve.Label, labelW), roleText)
			c.text(2+labelW+1, y, gaugeBar(f, barW), roleBox)
			value := number(v)
			c.text(2+labelW+1+barW+1+valueW-width(value), y, value, roleNote)
			y++
		}
	}
	return c, nil
}
