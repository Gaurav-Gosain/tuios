// Ported from pele src/diagrams/journey/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package journey

import (
	"math"
	"strconv"
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

const (
	PAD_X      = 10
	PAD_Y      = 8
	MIN_TEXT   = 124
	MAX_TEXT   = 200
	GAP        = 12
	BAND_PAD   = 6
	GUTTER     = 24
	LEVEL      = 26
	FACE       = 11
	DOT        = 5
	DOT_STEP   = 13
	LEGEND_GAP = 20
)

const EYES = `<circle cx="-3.6" cy="-2.8" r="1.3"/><circle cx="3.6" cy="-2.8" r="1.3"/>`

// MOUTHS is a switch rather than a map, which would allocate at init.
func MOUTHS(mood string) string {
	switch mood {
	case "happy":
		return "M-5,2A6,6 0 0 0 5,2"
	case "sad":
		return "M-5,6.5A6,6 0 0 1 5,6.5"
	}
	return "M-4.5,4.5H4.5"
}

func face(b *strings.Builder, x, y float64, mood string) {
	svg.WriteStrings(b, `<g class="pele-face pele-face-`, mood, `" transform="translate(`, svg.Num(x), `,`, svg.Num(y), `)">`,
		`<circle r="`, strconv.Itoa(FACE), `" fill="var(--_bg)" stroke="var(--_l)"/>`,
		`<g fill="var(--_l)">`, EYES, `</g>`,
		`<path d="`, MOUTHS(mood), `" fill="none" stroke="var(--_l)" stroke-linecap="round"/>`,
		`</g>`)
}

// mood is the face a score shows.
func mood(score float64) string {
	if score > 3 {
		return "happy"
	}
	if score < 3 {
		return "sad"
	}
	return "neutral"
}

func renderJourney(model *JourneyDb, config types.Config, options types.RenderOptions) types.Rendered {
	return common.TurnToFit(options, func(down bool) types.Rendered { return draw(model, config, options, down) })
}

type legendItem struct {
	name  string
	label text.Label
	x, y  float64
	h     float64
}

type run struct {
	start, count int
	label        *text.Label
}

func nonEmpty(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func draw(model *JourneyDb, _ types.Config, options types.RenderOptions, down bool) types.Rendered {
	num := svg.Num
	size := types.Or(options.FontSize, 16)
	small := js.Round(size * 0.875)
	tiny := js.Round(size * 0.75)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)
	if down {
		return verticalJourney(model, options)
	}
	tasks := model.tasks
	n := len(tasks)

	// `Task: 5:` names an actor with no name; Mermaid gives it a dot and an empty legend row.
	names := nonEmpty(journeyActors(tasks))
	actor := make(map[string]int, len(names))
	for i, name := range names {
		actor[name] = i
	}

	// Cards widen, up to a point, for a word that would not fit; past that the word is cut.
	textW := float64(MIN_TEXT)
	labels := make([]text.Label, n)
	for i, task := range tasks {
		labels[i] = common.PlainLabel(task.task, measurer, small, textW, 0)
	}
	widest := 0.0
	for _, label := range labels {
		widest = math.Max(widest, label.Width)
	}
	if widest > textW {
		textW = math.Min(math.Ceil(widest), MAX_TEXT)
		for i, task := range tasks {
			labels[i] = common.FitLabel(task.task, measurer, small, textW, 0)
		}
	}

	cardW := textW + 2*PAD_X
	pitch := cardW + GAP
	plotW := 0.0
	if n > 0 {
		plotW = float64(float64(n)*pitch) - GAP
	}

	lineH := js.Round(small * 1.5)
	limit := math.Max(plotW, 240)
	legend := make([]legendItem, 0, len(names))
	legendW := 0.0
	legendH := 0.0
	lx := 0.0
	for _, name := range names {
		label := common.FitLabel(name, measurer, small, limit-2*DOT-6, 0)
		w := 2*DOT + 6 + label.Width
		if lx > 0 && lx+w > limit {
			lx = 0
			legendH += 4
		}
		y := legendH
		if lx > 0 {
			y = legend[len(legend)-1].y
		}
		h := math.Max(label.Height, lineH)
		legend = append(legend, legendItem{name, label, lx, y, h})
		lx += w
		legendW = math.Max(legendW, lx)
		legendH = math.Max(legendH, y+h)
		lx += LEGEND_GAP
	}

	bodyW := 0.0
	if n > 0 {
		bodyW = GUTTER + plotW
	}
	title := common.FitLabel(model.title, measurer, size, math.Max(bodyW, 320), text.Bold)
	titleH := 0.0
	if title.Height > 0 {
		titleH = title.Height + 16
	}
	legendSpan := 0.0
	if n > 0 {
		legendSpan = GUTTER + legendW
	}
	width := math.Max(math.Max(bodyW, title.Width), legendSpan)
	left := pad + (width-bodyW)/2 + GUTTER
	top := pad + titleH

	// A band covers a run of tasks that share a section, as in Mermaid; a section with no tasks draws nothing.
	var runs []run
	bandH := 0.0
	for i := 0; i < n; {
		end := i + 1
		for end < n && tasks[end].section == tasks[i].section {
			end++
		}
		var label *text.Label
		if tasks[i].section != "" {
			l := common.FitLabel(tasks[i].section, measurer, small, float64(float64(end-i)*pitch)-GAP-2*PAD_X, text.Bold)
			label = &l
			bandH = math.Max(bandH, math.Max(l.Height, lineH)+2*BAND_PAD)
		}
		runs = append(runs, run{i, end - i, label})
		i = end
	}

	textH := lineH
	for _, label := range labels {
		textH = math.Max(textH, label.Height)
	}
	dotsH := 0.0
	if len(names) > 0 {
		dotsH = 2*DOT + 6
	}
	cardH := textH + dotsH + 2*PAD_Y
	cardsY := top
	if bandH > 0 {
		cardsY += bandH + BAND_PAD
	}
	chartTop := cardsY + cardH + FACE + 16
	chartBottom := chartTop + 4*LEVEL
	right := left + plotW

	var body strings.Builder
	bottom := top
	if n > 0 {
		var grid, scale strings.Builder
		for score := 5; score >= 1; score-- {
			y := chartTop + float64(float64(5-score)*LEVEL)
			svg.WriteStrings(&grid, "M", num(left-6), ",", num(y), "H", num(right))
			svg.WriteStrings(&scale, `<text x="`, num(left-12), `" y="`, num(y+float64(tiny*0.35)), `">`, strconv.Itoa(score), `</text>`)
		}
		svg.WriteStrings(&body, `<path class="pele-grid" d="`, grid.String(), `" fill="none" stroke="var(--_a)"/>`,
			`<g class="pele-scale" font-size="`, js.Num(tiny), `" text-anchor="end" fill="var(--_m)">`, scale.String(), `</g>`)

		var line, stems, faces strings.Builder
		cards := make([]string, n)
		for i, task := range tasks {
			x := left + float64(float64(i)*pitch)
			cx := x + cardW/2
			score := task.score
			level := 3.0
			if !math.IsNaN(score) {
				level = math.Min(5, math.Max(1, score))
			}
			y := chartTop + float64((5-level)*LEVEL)
			if i == 0 {
				line.WriteString("M")
			} else {
				line.WriteString("L")
			}
			svg.WriteStrings(&line, num(cx), ",", num(y))
			svg.WriteStrings(&stems, "M", num(cx), ",", num(cardsY+cardH), "V", num(y-FACE))
			face(&faces, cx, y, mood(score))

			var card strings.Builder
			people := nonEmpty(task.people)
			step := math.Min(DOT_STEP, (cardW-2*PAD_X-2*DOT)/float64(max(len(people)-1, 1)))
			svg.WriteStrings(&card, `<g class="pele-node pele-task" data-id="`, svg.EscText(js.Trim(task.task)), `">`,
				`<rect x="`, num(x), `" y="`, num(cardsY), `" width="`, num(cardW), `" height="`, num(cardH), `" fill="var(--_s)" stroke="var(--_b)"`, svg.ROUNDED, `/>`,
				svg.LabelSvg(labels[i], cx, cardsY+PAD_Y+textH/2, ` font-size="`+js.Num(small)+`"`, nil))
			for k, name := range people {
				svg.WriteStrings(&card, `<circle class="pele-actor" cx="`, num(cx+float64((float64(k)-float64(len(people)-1)/2)*step)), `" cy="`, num(cardsY+cardH-PAD_Y-DOT),
					`" r="`, strconv.Itoa(DOT), `" fill="`, svg.SeriesColor(actor[name]), `" stroke="var(--_s)">`,
					`<title>`, svg.EscText(name), `</title></circle>`)
			}
			card.WriteString("</g>")
			cards[i] = card.String()
		}
		svg.WriteStrings(&body, `<path class="pele-stems" d="`, stems.String(), `" fill="none" stroke="var(--_b)" stroke-dasharray="2 3"/>`)
		if n > 1 {
			svg.WriteStrings(&body, `<path class="pele-edge" d="`, line.String(), `" fill="none" stroke="var(--_l)" stroke-linejoin="round"/>`)
		}
		body.WriteString(faces.String())

		for _, r := range runs {
			if r.label == nil {
				for _, card := range cards[r.start : r.start+r.count] {
					body.WriteString(card)
				}
				continue
			}
			x := left + float64(float64(r.start)*pitch)
			w := float64(float64(r.count)*pitch) - GAP
			svg.WriteStrings(&body, `<g class="pele-cluster pele-section" data-id="`, svg.EscText(js.Trim(tasks[r.start].section)), `">`,
				`<rect x="`, num(x), `" y="`, num(top), `" width="`, num(w), `" height="`, num(bandH), `" fill="var(--_a)"`, svg.ROUNDED, `/>`,
				svg.LabelSvg(*r.label, x+w/2, top+bandH/2, ` font-size="`+js.Num(small)+`" font-weight="var(--_hw)"`, nil))
			for _, card := range cards[r.start : r.start+r.count] {
				body.WriteString(card)
			}
			body.WriteString("</g>")
		}
		bottom = chartBottom + FACE

		if len(legend) > 0 {
			x0 := left + (plotW-legendW)/2
			y0 := bottom + 16
			svg.WriteStrings(&body, `<g class="pele-legend" font-size="`, js.Num(small), `">`)
			for _, item := range legend {
				cy := y0 + item.y + item.h/2
				svg.WriteStrings(&body, `<g class="pele-legend-item" data-id="`, svg.EscText(item.name), `">`,
					`<circle cx="`, num(x0+item.x+DOT), `" cy="`, num(cy), `" r="`, strconv.Itoa(DOT), `" fill="`, svg.SeriesColor(actor[item.name]), `"/>`,
					svg.LabelSvg(item.label, x0+item.x+2*DOT+6+item.label.Width/2, cy, "", nil),
					`</g>`)
			}
			body.WriteString(`</g>`)
			bottom = y0 + legendH
		}
	}

	totalWidth := math.Ceil(width + float64(2*pad))
	totalHeight := math.Ceil(math.Max(bottom, pad) + pad)
	var geometry *types.Geometry
	if options.Geometry != nil {
		geometry = geometryOf(model, title, labels, runs, totalWidth, totalHeight)
	}
	return types.Rendered{
		Geometry: geometry,
		SVG: svg.SvgDocument("journey", totalWidth, totalHeight, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr},
			svg.LabelSvg(title, pad+width/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil), body.String()),
		Width:  totalWidth,
		Height: totalHeight,
		Links:  []types.LinkInfo{},
	}
}

func verticalJourney(model *JourneyDb, options types.RenderOptions) types.Rendered {
	num := svg.Num
	size := types.Or(options.FontSize, 16)
	small := js.Round(size * 0.875)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)
	maxWidth := 320.0
	if options.MaxWidth != nil {
		maxWidth = *options.MaxWidth
	}
	room := math.Max(140, maxWidth-float64(2*pad))
	title := common.FitLabel(model.title, measurer, size, room, text.Bold)
	names := nonEmpty(journeyActors(model.tasks))
	actors := make(map[string]int, len(names))
	for i, name := range names {
		actors[name] = i
	}
	y := pad
	if title.Height > 0 {
		y += title.Height + 16
	}
	section := ""
	started := false
	var body strings.Builder
	width := title.Width
	var runs []run
	labels := make([]text.Label, len(model.tasks))
	for index, task := range model.tasks {
		if !started || task.section != section {
			started = true
			section = task.section
			head := common.FitLabel(section, measurer, size, room-16, text.Bold)
			runs = append(runs, run{start: index})
			if head.Height > 0 {
				runs[len(runs)-1].label = &head
				body.WriteString(svg.LabelSvg(head, pad+8+head.Width/2, y+head.Height/2, ` class="pele-cluster-label" font-weight="var(--_hw)"`, nil))
				width = math.Max(width, head.Width+16)
				y += head.Height + 8
			}
		}
		label := common.FitLabel(task.task, measurer, small, room-72, 0)
		labels[index] = label
		runs[len(runs)-1].count++
		type person struct {
			name  string
			label text.Label
		}
		var people []person
		for _, name := range task.people {
			if name != "" {
				people = append(people, person{name, common.FitLabel(name, measurer, small, room-44, 0)})
			}
		}
		textH := math.Max(label.Height, 42)
		h := 2*PAD_Y + textH
		for _, p := range people {
			h += math.Max(16, p.label.Height) + 4
		}
		w := math.Max(room, label.Width+72)
		svg.WriteStrings(&body, `<g class="pele-node pele-journey-step" data-id="`, strconv.Itoa(index), `"><rect x="`, num(pad), `" y="`, num(y),
			`" width="`, num(w), `" height="`, num(h), `" fill="var(--_s)" stroke="var(--_b)"`, svg.ROUNDED, `/>`)

		body.WriteString(svg.LabelSvg(label, pad+PAD_X+label.Width/2, y+PAD_Y+label.Height/2, ` class="pele-label"`, nil))
		cx := pad + w - 28
		face(&body, cx, y+PAD_Y+FACE, mood(task.score))
		if !math.IsNaN(task.score) {
			svg.WriteStrings(&body, `<text class="pele-journey-score" x="`, num(cx), `" y="`, num(y+PAD_Y+2*FACE+small), `" font-size="`, js.Num(small),
				`" text-anchor="middle">`, num(math.Min(5, math.Max(1, task.score))), `</text>`)
		}
		py := y + PAD_Y + textH + 4
		for _, p := range people {
			ph := math.Max(16, p.label.Height)
			svg.WriteStrings(&body, `<circle class="pele-actor" cx="`, num(pad+16), `" cy="`, num(py+ph/2), `" r="`, strconv.Itoa(DOT), `" fill="`, svg.SeriesColor(actors[p.name]), `"/>`,
				svg.LabelSvg(p.label, pad+28+p.label.Width/2, py+ph/2, ` class="pele-journey-actor" font-size="`+js.Num(small)+`"`, nil))
			py += ph + 4
		}
		body.WriteString("</g>")
		y += h
		if index+1 < len(model.tasks) {
			svg.WriteStrings(&body, `<path class="pele-edge" d="M`, num(pad+w/2), ",", num(y), `v16" fill="none" stroke="var(--_l)"/>`)
		}
		y += 16
		width = math.Max(width, w)
	}
	totalWidth := math.Ceil(width + float64(2*pad))
	last := 0.0
	if len(model.tasks) > 0 {
		last = 16
	}
	height := math.Ceil(math.Max(pad+title.Height, y-last) + pad)
	var geometry *types.Geometry
	if options.Geometry != nil {
		geometry = geometryOf(model, title, labels, runs, totalWidth, height)
	}
	return types.Rendered{
		Geometry: geometry,
		SVG: svg.SvgDocument("journey", totalWidth, height, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr},
			svg.LabelSvg(title, totalWidth/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil), body.String()),
		Width:  totalWidth,
		Height: height,
		Links:  []types.LinkInfo{},
	}
}

// geometryOf is the journey as lists, for sketch/text.
func geometryOf(model *JourneyDb, title text.Label, labels []text.Label, runs []run, width, height float64) *types.Geometry {
	g := &types.GeoJourney{Title: title.Plain()}
	for _, r := range runs {
		group := types.GeoGroup{}
		if r.label != nil {
			group.Label = r.label.Plain()
		}
		for i := r.start; i < r.start+r.count; i++ {
			group.Periods = append(group.Periods, i)
		}
		g.Sections = append(g.Sections, group)
	}
	for i, task := range model.tasks {
		level := 3.0
		if !math.IsNaN(task.score) {
			level = math.Min(5, math.Max(1, task.score))
		}
		g.Tasks = append(g.Tasks, types.GeoStep{Label: labels[i].Plain(), Level: level, Mood: mood(task.score), People: nonEmpty(task.people)})
	}
	return &types.Geometry{Width: width, Height: height, Journey: g}
}
