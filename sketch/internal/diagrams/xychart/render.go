// Ported from pele src/diagrams/xychart/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package xychart

import (
	"math"
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

const (
	TICK       = 4
	SWATCH     = 12
	ROW        = 26
	LEGEND_GAP = 24
	LIMIT      = 1e300
)

type Mark struct {
	at    float64
	label text.Label
}

func record(value any) types.Config {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return types.Config{}
}

func on(config types.Config, key string, fallback bool) bool {
	if value, ok := config[key].(bool); ok {
		return value
	}
	return fallback
}

func dimension(value any, fallback float64) float64 {
	if v, ok := value.(float64); ok && v > 0 {
		return js.Round(math.Min(math.Max(v, 100), 4000))
	}
	return fallback
}

// finite is a value clamped to ±LIMIT, and false for NaN, which stands for undefined, and the infinities.
func finite(value float64) (float64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return math.Min(math.Max(value, -LIMIT), LIMIT), true
}

func widest(marks []Mark) float64 {
	width := 0.0
	for _, mark := range marks {
		width = math.Max(width, mark.label.Width)
	}
	return width
}

// aligned writes a label with its left edge, center or right edge at x. A plain one-line label keeps
// its place when the text is wider or narrower than measured.
func aligned(b *strings.Builder, label text.Label, x, y float64, anchor string) {
	if len(label.Lines) == 1 {
		line := label.Lines[0]
		if len(line) == 1 && line[0].Icon == "" && line[0].Style == 0 {
			b.WriteString(`<text x="`)
			svg.WriteNum(b, x)
			b.WriteString(`" y="`)
			svg.WriteNum(b, y+float64(label.Size*0.35))
			svg.WriteStrings(b, `" text-anchor="`, anchor, `">`, svg.Esc(line[0].Text), `</text>`)
			return
		}
	}
	cx := x
	if anchor == "start" {
		cx = x + label.Width/2
	} else if anchor == "end" {
		cx = x + -label.Width/2
	}
	b.WriteString(svg.LabelSvg(label, cx, y, "", nil))
}

func renderXyChart(model *XyChartDb, config types.Config, options types.RenderOptions) types.Rendered {
	if len(model.plots) == 0 {
		types.Throw("No Plot to render, please provide a plot with some data", types.CodeSemantic, types.ErrorInfo{Type: "xychart"})
	}
	num := svg.Num
	chart := record(config["xyChart"])
	size := types.Or(options.FontSize, 16)
	small := js.Round(size * 0.875)
	tiny := js.Round(size * 0.75)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)
	natural := dimension(chart["width"], 700)
	width := common.CapWidth(options, natural, 240, 0)
	// A narrower chart is not as much shorter, so that the plot keeps room for its marks.
	height := js.Round(dimension(chart["height"], 500) * math.Max(0.6, width/natural))
	orientation := any(model.orientation)
	if model.orientation == "" {
		orientation = chart["chartOrientation"]
	}
	horizontal := orientation == "horizontal"
	// The value of a bar is always written past its end, which is what showDataLabelOutsideBar asks
	// for in Mermaid. Inside, whether it fits changes from bar to bar and with the width of the chart.
	showValues := on(chart, "showDataLabel", false)
	x := &model.xAxis
	band := x.typ == "band"
	plots := model.plots
	label := func(raw string, fontSize, max float64) text.Label {
		return text.LayoutLabel(raw, false, measurer, fontSize, max, 0)
	}
	lineHeight := label("0", small, 4000).Height
	tinyHeight := label("0", tiny, 4000).Height

	titleText := ""
	if on(chart, "showTitle", true) {
		titleText = model.title
	}
	title := text.LayoutLabel(titleText, false, measurer, size, width-float64(2*pad), text.Bold)
	titleSpace := 0.0
	if title.Height > 0 {
		titleSpace = title.Height + 12
	}

	type legendItem struct {
		index int
		label text.Label
	}
	var legend []legendItem
	legendWidth := 0.0
	if on(chart, "showLegend", true) {
		for index, plot := range plots {
			if plot.title == "" {
				continue
			}
			l := label(plot.title, small, width*0.3)
			legend = append(legend, legendItem{index, l})
			legendWidth = math.Max(legendWidth, l.Width+SWATCH+8)
		}
	}
	legendSpace := 0.0
	if len(legend) > 0 {
		legendSpace = legendWidth + LEGEND_GAP
	}

	// The value range: as written, or taken from the data, where bars stand on zero and the
	// range grows to whole steps.
	bars := 0
	for _, plot := range plots {
		if plot.typ == "bar" {
			bars++
		}
	}
	lo, ok := finite(model.yAxis.min)
	if !ok {
		lo = math.Inf(1)
	}
	hi, ok := finite(model.yAxis.max)
	if !ok {
		hi = math.Inf(-1)
	}
	exact := model.hasSetYAxis && !math.IsInf(lo, 0) && !math.IsInf(hi, 0) && lo != hi
	if !exact {
		lo = math.Inf(1)
		hi = math.Inf(-1)
		for _, plot := range plots {
			for _, raw := range plot.data {
				value, ok := finite(raw)
				if !ok {
					continue
				}
				if value < lo {
					lo = value
				}
				if value > hi {
					hi = value
				}
			}
		}
		if lo > hi {
			lo, hi = 0, 0
		}
		if bars > 0 {
			lo = math.Min(lo, 0)
			hi = math.Max(hi, 0)
		}
		if lo == hi {
			if lo == 0 {
				hi = 1
			} else {
				lo -= math.Abs(lo) / 2
				hi += math.Abs(hi) / 2
			}
		}
	}

	valueMarks := func(length, spacing float64) []Mark {
		step := tickStep(math.Abs(hi-lo), math.Max(2, math.Min(10, math.Floor(length/spacing))))
		if !exact {
			lo = tickValue(step, math.Floor(tickIndex(step, lo)+1e-9))
			hi = tickValue(step, math.Ceil(tickIndex(step, hi)-1e-9))
		}
		values := ticksBetween(math.Min(lo, hi), math.Max(lo, hi), step)
		marks := make([]Mark, len(values))
		for i, value := range values {
			marks[i] = Mark{at: (value - lo) / (hi - lo), label: label(js.Num(value), small, 4000)}
		}
		return marks
	}

	// Places along the category axis are fractions of its length. Categories and bars sit in the
	// middle of equal slots; the points of a line on a numeric axis run from end to end.
	count := 1
	if band {
		count = max(len(x.categories), 1)
	} else {
		for _, plot := range plots {
			count = max(count, plot.length)
		}
	}
	slot := 1 / float64(count)
	inset := 0.0
	if band || bars > 0 {
		inset = slot / 2
	}
	x0, has0 := 0.0, false
	x1, has1 := 0.0, false
	if x.typ == "linear" {
		x0, has0 = finite(x.min)
		x1, has1 = finite(x.max)
	}
	ranged := has0 && has1 && x0 != x1
	place := func(i, n int) float64 {
		if band {
			return (float64(i) + 0.5) * slot
		}
		at := 0.5
		if n > 1 {
			at = float64(i) / float64(n-1)
		} else if ranged {
			at = 0
		}
		return inset + float64(at*(1-float64(2*inset)))
	}

	categoryConf := record(chart["xAxis"])
	valueConf := record(chart["yAxis"])
	leftConf, bottomConf := valueConf, categoryConf
	if horizontal {
		leftConf, bottomConf = categoryConf, valueConf
	}
	leftTitleText, bottomTitleText := "", ""
	if on(leftConf, "showTitle", true) {
		if horizontal {
			leftTitleText = x.title
		} else {
			leftTitleText = model.yAxis.title
		}
	}
	if on(bottomConf, "showTitle", true) {
		if horizontal {
			bottomTitleText = model.yAxis.title
		} else {
			bottomTitleText = x.title
		}
	}
	leftTitle := label(leftTitleText, small, height*0.8)
	bottomTitle := label(bottomTitleText, small, width*0.8)
	leftTick := 0.0
	if on(leftConf, "showTick", true) {
		leftTick = TICK
	}
	bottomTick := 0.0
	if on(bottomConf, "showTick", true) {
		bottomTick = TICK
	}
	leftLabels := on(leftConf, "showLabel", true)
	bottomLabels := on(bottomConf, "showLabel", true)

	wrapAt := math.Max(48, float64((width-float64(2*pad)-64-legendSpace)*slot)-8)
	if horizontal {
		wrapAt = width * 0.3
	}
	var categories []text.Label
	if band && on(categoryConf, "showLabel", true) {
		categories = make([]text.Label, len(x.categories))
		for i, category := range x.categories {
			categories[i] = label(category, small, wrapAt)
		}
	}
	categoryWidth := 0.0
	categoryHeight := 0.0
	for _, l := range categories {
		categoryWidth = math.Max(categoryWidth, l.Width)
		categoryHeight = math.Max(categoryHeight, l.Height)
	}

	// The labelled places of the category axis. Labels that would run into each other are thinned out.
	categoryMarks := func(length float64) []Mark {
		var marks []Mark
		if band {
			need := categoryWidth + 12
			if len(categories) == 0 {
				need = 8
			} else if horizontal {
				need = categoryHeight
			}
			every := int(math.Max(1, math.Ceil(need/(length*slot))))
			for i := 0; i < len(x.categories); i += every {
				l := text.EMPTY
				if i < len(categories) {
					l = categories[i]
				}
				marks = append(marks, Mark{at: (float64(i) + 0.5) * slot, label: l})
			}
		} else if ranged {
			spacing := 80.0
			if horizontal {
				spacing = 32
			}
			step := tickStep(math.Abs(x1-x0), math.Max(2, math.Min(10, math.Floor(length/spacing))))
			for _, value := range ticksBetween(math.Min(x0, x1), math.Max(x0, x1), step) {
				marks = append(marks, Mark{at: inset + float64(((value-x0)/(x1-x0))*(1-float64(2*inset))), label: label(js.Num(value), small, 4000)})
			}
		} else if has0 {
			marks = append(marks, Mark{at: 0.5, label: label(js.Num(x0), small, 4000)})
		}
		return marks
	}

	// Room past the end of the value axis for what is written beside a point or a bar.
	pointLabelWidth := 0.0
	valueWidth := 0.0
	for _, plot := range plots {
		for _, l := range plot.pointLabels {
			pointLabelWidth = math.Max(pointLabelWidth, measurer.Width(l, tiny, 0))
		}
		if showValues && horizontal && plot.typ == "bar" {
			for _, value := range plot.data {
				s := ""
				if v, ok := finite(value); ok {
					s = js.Num(v)
				}
				valueWidth = math.Max(valueWidth, measurer.Width(s, tiny, 0))
			}
		}
	}

	bottomLabelHeight := 0.0
	if bottomLabels {
		bottomLabelHeight = categoryHeight
		if horizontal || !band {
			bottomLabelHeight = lineHeight
		}
	}
	bottomSpace := bottomTick
	if bottomLabelHeight > 0 {
		bottomSpace += bottomLabelHeight + 4
	}
	if bottomTitle.Height > 0 {
		bottomSpace += bottomTitle.Height + 4
	}
	headroom := 0.0
	if !horizontal {
		if pointLabelWidth > 0 || showValues {
			headroom = tinyHeight + 4
		} else if leftLabels {
			headroom = lineHeight / 2
		}
	}
	top := pad + titleSpace + headroom
	plotHeight := math.Max(1, height-pad-bottomSpace-top)
	bottom := top + plotHeight

	var leftMarks []Mark
	if horizontal {
		leftMarks = categoryMarks(plotHeight)
	} else {
		leftMarks = valueMarks(plotHeight, 56)
	}
	leftLabelWidth := 0.0
	if leftLabels {
		leftLabelWidth = widest(leftMarks)
	}
	left := pad
	if leftTitle.Height > 0 {
		left += leftTitle.Height + 6
	}
	if leftLabelWidth > 0 {
		left += leftLabelWidth + 6
	}
	left += leftTick
	beside := 0.0
	if pointLabelWidth > 0 {
		beside = pointLabelWidth + 12
	}
	if valueWidth > 0 {
		beside = math.Max(beside, valueWidth+8)
	}
	reach := 0.0
	if horizontal {
		reach = math.Min(width*0.25, beside)
	}
	plotWidth := math.Max(1, width-pad-left-legendSpace-reach)
	var bottomMarks []Mark
	if horizontal {
		bottomMarks = valueMarks(plotWidth, 80)
	} else {
		bottomMarks = categoryMarks(plotWidth)
	}
	if bottomLabels && legendSpace+reach == 0 {
		over := 0.0
		for _, mark := range bottomMarks {
			over = math.Max(over, float64(mark.at*plotWidth)+mark.label.Width/2-plotWidth)
		}
		plotWidth = math.Max(1, plotWidth-over)
	}
	right := left + plotWidth

	px := func(c, v float64) float64 {
		if horizontal {
			return left + float64(v*plotWidth)
		}
		return left + float64(c*plotWidth)
	}
	py := func(c, v float64) float64 {
		if horizontal {
			return top + float64(c*plotHeight)
		}
		return bottom - float64(v*plotHeight)
	}
	scale := func(value float64) float64 { return (value - lo) / (hi - lo) }
	clamp := func(v float64) float64 { return math.Min(math.Max(v, 0), 1) }

	// rule is a line across the plot at a place on the value axis.
	rule := func(b *strings.Builder, v float64) {
		if horizontal {
			svg.WriteStrings(b, "M", num(px(0, v)), ",", num(top), "V", num(bottom))
		} else {
			svg.WriteStrings(b, "M", num(left), ",", num(py(0, v)), "H", num(right))
		}
	}

	var grid strings.Builder
	gridMarks := leftMarks
	if horizontal {
		gridMarks = bottomMarks
	}
	for _, mark := range gridMarks {
		rule(&grid, mark.at)
	}

	var axis, labels strings.Builder
	if on(leftConf, "showAxisLine", true) {
		svg.WriteStrings(&axis, "M", num(left), ",", num(top), "V", num(bottom))
	}
	if on(bottomConf, "showAxisLine", true) {
		svg.WriteStrings(&axis, "M", num(left), ",", num(bottom), "H", num(right))
	}
	for _, mark := range leftMarks {
		y := bottom - float64(mark.at*plotHeight)
		if horizontal {
			y = top + float64(mark.at*plotHeight)
		}
		if leftTick != 0 {
			svg.WriteStrings(&axis, "M", num(left), ",", num(y), "h", js.Num(-leftTick))
		}
		if leftLabels {
			aligned(&labels, mark.label, left-leftTick-6, y, "end")
		}
	}
	for _, mark := range bottomMarks {
		at := left + float64(mark.at*plotWidth)
		if bottomTick != 0 {
			svg.WriteStrings(&axis, "M", num(at), ",", num(bottom), "v", js.Num(bottomTick))
		}
		if bottomLabels {
			aligned(&labels, mark.label, at, bottom+bottomTick+4+mark.label.Height/2, "middle")
		}
	}

	spanLength := plotWidth
	if horizontal {
		spanLength = plotHeight
	}
	span := spanLength * slot
	group := span * 0.7
	thickness := group / math.Max(float64(bars), 1)
	gap := 0.0
	if bars > 1 {
		gap = math.Min(2, thickness*0.2)
	}
	base := clamp(scale(0))
	// Bars that go both ways get a line to stand on.
	if bars > 0 && base > 0 && base < 1 {
		rule(&axis, base)
	}
	// Lines are drawn over the bars wherever they were declared, as a bar would hide part of one
	// under it. A series keeps the color of its place in the source.
	var series, lines, outside, pointLabels strings.Builder
	// Not part of pele: the point labels as plain text, by series and value, for the text drawing.
	var pointText [][]string
	bar := 0
	for index, plot := range plots {
		color := svg.SeriesColor(index)
		id := ""
		if plot.title != "" {
			id = ` data-id="` + svg.EscText(plot.title) + `"`
		}
		// A series declared before the categories can hold more values than there are categories.
		n := plot.length
		if band {
			n = min(plot.length, count)
		}
		if plot.typ == "bar" {
			offset := float64((float64(bar)-float64(bars-1)/2)*thickness) - (thickness-gap)/2
			bar++
			svg.WriteStrings(&series, `<g class="pele-series pele-bars"`, id, ` fill="`, color, `">`)
			for i := 0; i < n; i++ {
				value, ok := finite(plot.at(i))
				if !ok {
					continue
				}
				c := place(i, n)
				v := clamp(scale(value))
				from := math.Min(base, v)
				to := math.Max(base, v)
				var bx, by, bw, bh float64
				if horizontal {
					bx = px(c, from)
					by = py(c, 0) + offset
					bw = (to - from) * plotWidth
					bh = thickness - gap
				} else {
					bx = px(c, 0) + offset
					by = py(c, to)
					bw = thickness - gap
					bh = (to - from) * plotHeight
				}
				svg.WriteStrings(&series, `<rect x="`, num(bx), `" y="`, num(by), `" width="`, num(bw), `" height="`, num(bh), `"/>`)
				if !showValues {
					continue
				}
				valueLabel := js.Num(value)
				w := measurer.Width(valueLabel, tiny, 0)
				// Towards the end of the bar: 1 when it runs up or right from the baseline, -1 the other way.
				out := -1.0
				if v >= base {
					out = 1
				}
				tx := bx + bw/2
				ty := by + bh/2
				anchor := "middle"
				if horizontal {
					if bh < tiny {
						continue
					}
					if out > 0 {
						tx = bx + bw + float64(out*6)
						anchor = "start"
					} else {
						tx = bx + float64(out*6)
						anchor = "end"
					}
				} else {
					if w+2 > span/float64(bars) {
						continue
					}
					if out > 0 {
						ty = by - float64(out*(tinyHeight/2+2))
					} else {
						ty = by + bh - float64(out*(tinyHeight/2+2))
					}
				}
				svg.WriteStrings(&outside, `<text x="`, num(tx), `" y="`, num(ty+float64(tiny*0.35)), `" text-anchor="`, anchor, `">`, valueLabel, `</text>`)
			}
			series.WriteString(`</g>`)
			continue
		}

		// A line is cut where it leaves the value range, so it stays inside the plot.
		var d, dots strings.Builder
		pen := false
		pc := 0.0
		pv := math.NaN()
		for i := 0; i < n; i++ {
			value, ok := finite(plot.at(i))
			if !ok {
				pv = math.NaN()
				continue
			}
			c := place(i, n)
			v := scale(value)
			visible := v >= 0 && v <= 1
			if math.IsNaN(pv) {
				if visible {
					svg.WriteStrings(&d, "M", num(px(c, v)), ",", num(py(c, v)))
				}
				pen = visible
			} else {
				dv := v - pv
				t0 := 0.0
				t1 := 1.0
				if dv != 0 {
					a := -pv / dv
					b := (1 - pv) / dv
					t0 = math.Max(t0, math.Min(a, b))
					t1 = math.Min(t1, math.Max(a, b))
				} else if !visible {
					t0 = 2
				}
				if t0 <= t1 {
					c0 := pc + float64((c-pc)*t0)
					v0 := pv + float64(dv*t0)
					c1 := pc + float64((c-pc)*t1)
					v1 := pv + float64(dv*t1)
					if !pen || t0 > 0 {
						svg.WriteStrings(&d, "M", num(px(c0, v0)), ",", num(py(c0, v0)))
					}
					svg.WriteStrings(&d, "L", num(px(c1, v1)), ",", num(py(c1, v1)))
				}
				pen = visible
			}
			pc = c
			pv = v
			if !visible {
				continue
			}
			cx := px(c, v)
			cy := py(c, v)
			if span >= 12 {
				svg.WriteStrings(&dots, `<circle cx="`, num(cx), `" cy="`, num(cy), `" r="3"/>`)
			}
			raw := ""
			if i < len(plot.pointLabels) {
				raw = plot.pointLabels[i]
			}
			l := label(raw, tiny, 4000)
			if l.Height > 0 && options.Geometry != nil {
				if pointText == nil {
					pointText = make([][]string, len(plots))
				}
				if pointText[index] == nil {
					pointText[index] = make([]string, n)
				}
				pointText[index][i] = plainLabel(l)
			}
			if l.Height > 0 {
				half := l.Width / 2
				if horizontal {
					aligned(&pointLabels, l, cx+10, cy, "start")
				} else {
					aligned(&pointLabels, l, math.Max(math.Min(cx, right-half), left+half), cy-6-l.Height/2, "middle")
				}
			}
		}
		svg.WriteStrings(&lines, `<g class="pele-series pele-line"`, id, ` fill="`, color, `">`)
		if d.Len() > 0 {
			svg.WriteStrings(&lines, `<path d="`, d.String(), `" fill="none" stroke="`, color, `" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>`)
		}
		svg.WriteStrings(&lines, dots.String(), "</g>")
	}

	var titles strings.Builder
	if leftTitle.Height > 0 {
		svg.WriteStrings(&titles, `<g transform="translate(`, num(pad+leftTitle.Height/2), ",", num(top+plotHeight/2), `) rotate(-90)">`,
			svg.LabelSvg(leftTitle, 0, 0, "", nil), "</g>")
	}
	titles.WriteString(svg.LabelSvg(bottomTitle, left+plotWidth/2, height-pad-bottomTitle.Height/2, "", nil))

	var legendOut strings.Builder
	legendHeight := 0.0
	for _, item := range legend {
		legendHeight += math.Max(ROW, item.label.Height+6)
	}
	ly := top + math.Max(0, (plotHeight-legendHeight)/2)
	lx := right + reach + LEGEND_GAP
	for _, item := range legend {
		row := math.Max(ROW, item.label.Height+6)
		if ly+row > height {
			break
		}
		y := ly + row/2
		color := svg.SeriesColor(item.index)
		svg.WriteStrings(&legendOut, `<g class="pele-legend-item" data-id="`, svg.EscText(plots[item.index].title), `">`)
		if plots[item.index].typ == "bar" {
			svg.WriteStrings(&legendOut, `<rect x="`, num(lx), `" y="`, num(y-SWATCH/2), `" width="12" height="12" rx="2" fill="`, color, `"/>`)
		} else {
			svg.WriteStrings(&legendOut, `<path d="M`, num(lx), ",", num(y), `h12" stroke="`, color, `" stroke-width="2" stroke-linecap="round"/>`)
		}
		svg.WriteStrings(&legendOut, svg.LabelSvg(item.label, lx+SWATCH+8+item.label.Width/2, y, "", nil), "</g>")
		ly += row
	}

	parts := []string{svg.LabelSvg(title, width/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil)}
	if grid.Len() > 0 {
		parts = append(parts, `<path class="pele-grid" d="`+grid.String()+`" stroke="var(--_a)"/>`)
	}
	parts = append(parts, `<g class="pele-plot">`+series.String()+lines.String()+`</g>`)
	if axis.Len() > 0 {
		parts = append(parts, `<path class="pele-axis" d="`+axis.String()+`" fill="none" stroke="var(--_b)"/>`)
	}
	if labels.Len() > 0 {
		parts = append(parts, `<g class="pele-axis-labels" font-size="`+js.Num(small)+`" fill="var(--_m)">`+labels.String()+`</g>`)
	}
	if titles.Len() > 0 {
		parts = append(parts, `<g class="pele-axis-titles" font-size="`+js.Num(small)+`">`+titles.String()+`</g>`)
	}
	if outside.Len() > 0 {
		parts = append(parts, `<g class="pele-data-labels" font-size="`+js.Num(tiny)+`">`+outside.String()+`</g>`)
	}
	if pointLabels.Len() > 0 {
		parts = append(parts, `<g class="pele-point-labels" font-size="`+js.Num(tiny)+`">`+pointLabels.String()+`</g>`)
	}
	if legendOut.Len() > 0 {
		parts = append(parts, `<g class="pele-legend" font-size="`+js.Num(small)+`">`+legendOut.String()+`</g>`)
	}

	var geometry *types.Geometry
	if options.Geometry != nil {
		names := make([]string, len(plots))
		for _, item := range legend {
			names[item.index] = plainLabel(item.label)
		}
		geometry = &types.Geometry{Width: width, Height: height,
			XY: geometryOf(model, title, names, valueMarksOf(horizontal, leftMarks, bottomMarks),
				categoryMarksOf(horizontal, band, leftMarks, bottomMarks, categories, slot),
				leftTitle, bottomTitle, horizontal, count, base, place, scale, showValues, pointText)}
	}
	return types.Rendered{
		Geometry: geometry,
		SVG:      svg.SvgDocument("xychart", width, height, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr}, parts...),
		Width:    width,
		Height:   height,
		Links:    []types.LinkInfo{},
	}
}

// Not part of pele: the chart as lists, for a text drawing (sketch/text).

func plainLabel(l text.Label) string { return strings.Join(l.Plain(), " ") }

func valueMarksOf(horizontal bool, leftMarks, bottomMarks []Mark) []types.GeoMark {
	marks := leftMarks
	if horizontal {
		marks = bottomMarks
	}
	out := make([]types.GeoMark, len(marks))
	for i, m := range marks {
		out[i] = types.GeoMark{At: m.at, Label: plainLabel(m.label)}
	}
	return out
}

// categoryMarksOf gives every category of a band axis, where the SVG thins them
// out by its own measure, and the ticks of a numeric axis.
func categoryMarksOf(horizontal, band bool, leftMarks, bottomMarks []Mark, categories []text.Label, slot float64) []types.GeoMark {
	if band {
		out := make([]types.GeoMark, len(categories))
		for i, l := range categories {
			out[i] = types.GeoMark{At: (float64(i) + 0.5) * slot, Label: plainLabel(l)}
		}
		return out
	}
	marks := bottomMarks
	if horizontal {
		marks = leftMarks
	}
	out := make([]types.GeoMark, len(marks))
	for i, m := range marks {
		out[i] = types.GeoMark{At: m.at, Label: plainLabel(m.label)}
	}
	return out
}

func geometryOf(model *XyChartDb, title text.Label, names []string, ticks, marks []types.GeoMark,
	leftTitle, bottomTitle text.Label, horizontal bool, count int, base float64,
	place func(i, n int) float64, scale func(float64) float64, showValues bool, pointText [][]string) *types.GeoXY {
	g := &types.GeoXY{Title: title.Plain(), Horizontal: horizontal, Ticks: ticks, Marks: marks, Slots: count, Base: base}
	g.Band = model.xAxis.typ == "band"
	g.ValueTitle, g.CategoryTitle = plainLabel(leftTitle), plainLabel(bottomTitle)
	if horizontal {
		g.ValueTitle, g.CategoryTitle = g.CategoryTitle, g.ValueTitle
	}
	band := g.Band
	for index, plot := range model.plots {
		s := types.GeoSeries{Bar: plot.typ == "bar", Name: names[index]}
		n := plot.length
		if band {
			n = min(n, count)
		}
		gap := false
		for i := 0; i < n; i++ {
			value, ok := finite(plot.at(i))
			if !ok {
				gap = true
				continue
			}
			p := types.GeoPoint{At: place(i, n), Value: scale(value), Gap: gap}
			if s.Bar && showValues {
				p.Label = js.Num(value)
			} else if index < len(pointText) && i < len(pointText[index]) {
				p.Label = pointText[index][i]
			}
			s.Points = append(s.Points, p)
			gap = false
		}
		g.Series = append(g.Series, s)
	}
	return g
}
