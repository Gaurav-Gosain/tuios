// Ported from pele src/diagrams/timeline/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package timeline

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
	PAD_X         = 12
	PAD_Y         = 8
	MIN_TEXT      = 152
	MAX_TEXT      = 216
	EVENT_TEXT_TD = 264
	GAP           = 20
	AXIS          = 20
	SPINE         = 14
	EVENT_GAP     = 8
	SECTION_GAP   = 8
	TAIL          = 12
	ON_COLOR      = ` fill="var(--_bg)"`
	LINE          = ` fill="none" stroke="var(--_b)"`
)

type Group struct {
	section int
	periods []int
}

func box(b *strings.Builder, x, y, w, h float64, attrs string) {
	svg.WriteStrings(b, `<rect x="`, svg.Num(x), `" y="`, svg.Num(y), `" width="`, svg.Num(w), `" height="`, svg.Num(h), `"`, svg.Rounded(attrs), `/>`)

}

func renderTimeline(model *TimelineDb, config types.Config, options types.RenderOptions) types.Rendered {
	if model.direction == "TD" {
		return draw(model, config, options, true)
	}
	return common.TurnToFit(options, func(down bool) types.Rendered { return draw(model, config, options, down) })
}

func draw(model *TimelineDb, config types.Config, options types.RenderOptions, vertical bool) types.Rendered {
	num := svg.Num
	size := types.Or(options.FontSize, 16)
	small := js.Round(size * 0.875)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)
	multicolor := true
	if conf, ok := config["timeline"].(map[string]any); ok {
		if v, ok := conf["disableMulticolor"].(bool); ok && v {
			multicolor = false
		}
	}
	periods := model.periods
	hasSections := len(model.sections) > 0

	// Mermaid draws nothing for periods that come before the first section; here they lead, in a neutral color.
	groups := make([]Group, len(model.sections))
	for section := range groups {
		groups[section].section = section
	}
	leading := Group{section: -1}
	for i, period := range periods {
		if period.sectionIndex >= 0 && period.sectionIndex < len(groups) {
			groups[period.sectionIndex].periods = append(groups[period.sectionIndex].periods, i)
		} else {
			leading.periods = append(leading.periods, i)
		}
	}
	if len(leading.periods) > 0 {
		groups = append([]Group{leading}, groups...)
	}

	// Columns widen, up to a point, for a word that would not fit; past that the word is cut.
	// Running down, the two columns share the width the host has.
	room := math.Inf(1)
	if vertical && options.MaxWidth != nil && *options.MaxWidth > 0 {
		room = *options.MaxWidth - float64(2*pad)
	}
	const taken = AXIS + SPINE*2 + 4*PAD_X
	periodText := math.Min(MIN_TEXT, math.Max(80, js.Round((room-taken)*0.38)))
	eventText := math.Min(EVENT_TEXT_TD, math.Max(110, room-taken-periodText))
	textW := periodText
	eventTextW := func() float64 {
		if vertical {
			return eventText
		}
		return textW
	}
	labels := func(fit bool) ([]text.Label, [][]text.Label) {
		layout := common.PlainLabel
		if fit {
			layout = common.FitLabel
		}
		periodLabels := make([]text.Label, len(periods))
		eventLabels := make([][]text.Label, len(periods))
		for i, p := range periods {
			periodLabels[i] = layout(p.text, measurer, size, textW, 0)
			eventLabels[i] = make([]text.Label, len(p.events))
			for k, e := range p.events {
				eventLabels[i][k] = layout(e, measurer, small, eventTextW(), 0)
			}
		}
		return periodLabels, eventLabels
	}
	periodLabels, eventLabels := labels(false)
	widest := 0.0
	for _, label := range periodLabels {
		widest = math.Max(widest, label.Width)
	}
	if !vertical {
		for _, ls := range eventLabels {
			for _, label := range ls {
				widest = math.Max(widest, label.Width)
			}
		}
	}
	overflow := widest > textW
	if vertical {
		for _, ls := range eventLabels {
			for _, label := range ls {
				overflow = overflow || label.Width > eventText
			}
		}
	}
	if overflow {
		limit := periodText
		if math.IsInf(room, 1) {
			limit = MAX_TEXT
		}
		textW = math.Min(math.Max(math.Ceil(widest), periodText), limit)
		periodLabels, eventLabels = labels(true)
	}

	lineHeight := js.Round(size * 1.5)
	colW := textW + 2*PAD_X
	eventW := eventTextW() + 2*PAD_X
	chipHeight := func(label text.Label) float64 { return math.Max(label.Height, lineHeight) + 2*PAD_Y }
	eventHeight := func(label text.Label) float64 { return math.Max(label.Height, js.Round(small*1.5)) + 2*PAD_Y }
	colorOf := func(group Group, period int) string {
		if group.section >= 0 {
			return svg.SeriesColor(group.section)
		}
		if hasSections {
			return "var(--_m)"
		}
		if multicolor {
			return svg.SeriesColor(period)
		}
		return svg.SeriesColor(0)
	}

	hasEvents := false
	for _, ls := range eventLabels {
		if len(ls) > 0 {
			hasEvents = true
		}
	}
	slots := 0
	for _, group := range groups {
		slots += max(len(group.periods), 1)
	}

	pitch := colW + GAP
	drawn := len(periods) > 0
	bodyW := 0.0
	if vertical {
		bodyW = colW
		if hasEvents {
			bodyW += AXIS + SPINE*2 + eventW
		} else if drawn {
			bodyW += AXIS + TAIL
		}
	} else if slots > 0 {
		bodyW = float64(float64(slots)*pitch) - GAP
		if drawn {
			bodyW += TAIL + svg.ARROW
		}
	}
	title := common.FitLabel(model.title, measurer, size, math.Max(bodyW, 320), text.Bold)
	titleH := 0.0
	if title.Height > 0 {
		titleH = title.Height + 16
	}
	width := math.Max(bodyW, title.Width)
	left := pad + (width-bodyW)/2
	top := pad + titleH

	sectionLabel := func(group Group, w float64) text.Label {
		return common.FitLabel(model.sections[group.section], measurer, size, w-2*PAD_X, text.Bold)
	}
	var body strings.Builder
	header := func(group Group, label text.Label, x, y, w, h float64) {
		box(&body, x, y, w, h, ` fill="`+svg.SeriesColor(group.section)+`"`)
		body.WriteString(svg.LabelSvg(label, x+w/2, y+h/2, ` font-weight="var(--_hw)"`+ON_COLOR, nil))
	}
	period := func(i int, color string, x, y, h float64) {
		box(&body, x, y, colW, h, ` fill="`+color+`"`)
		body.WriteString(svg.LabelSvg(periodLabels[i], x+colW/2, y+h/2, ON_COLOR, nil))
	}
	event := func(b *strings.Builder, label text.Label, color string, x, y, h float64) {
		b.WriteString(`<g class="pele-node pele-event">`)
		box(b, x, y, eventW, h, ` fill="`+color+`" fill-opacity="0.16"`)
		svg.WriteStrings(b, svg.LabelSvg(label, x+eventW/2, y+h/2, ` font-size="`+js.Num(small)+`"`, nil), "</g>")
	}
	dot := func(x, y float64, color string) {
		svg.WriteStrings(&body, `<circle class="pele-dot" cx="`, num(x), `" cy="`, num(y), `" r="4" fill="`, color, `" stroke="var(--_bg)" stroke-width="2"/>`)
	}
	open := func(group Group) {
		if group.section >= 0 {
			svg.WriteStrings(&body, `<g class="pele-cluster pele-section" data-id="`, svg.EscText(js.Trim(model.sections[group.section])), `">`)
		}
	}
	openPeriod := func(i int) {
		svg.WriteStrings(&body, `<g class="pele-node pele-period" data-id="`, svg.EscText(js.Trim(periods[i].text)), `">`)
	}

	// The labels of the groups as drawn, for the geometry.
	groupLabels := make([]text.Label, len(groups))
	var axis string
	var height float64

	if vertical {
		axisX := left + colW + AXIS
		spineX := axisX + SPINE
		eventsX := spineX + SPINE
		y := top
		for g, group := range groups {
			open(group)
			if group.section >= 0 {
				label := sectionLabel(group, bodyW)
				groupLabels[g] = label
				h := chipHeight(label)
				header(group, label, left, y, bodyW, h)
				y += h + SECTION_GAP + 4
			}
			for _, i := range group.periods {
				color := colorOf(group, i)
				h := chipHeight(periodLabels[i])
				// A period and its first event share one center line, so the line between them runs straight.
				first := h
				if len(eventLabels[i]) > 0 {
					first = eventHeight(eventLabels[i][0])
				}
				cy := y + math.Max(h, first)/2
				var d, cards strings.Builder
				svg.WriteStrings(&d, "M", num(left+colW), ",", num(cy), "H", num(axisX))
				ey := cy - first/2
				last := cy
				for _, label := range eventLabels[i] {
					eh := eventHeight(label)
					last = ey + eh/2
					svg.WriteStrings(&d, "M", num(spineX), ",", num(last), "H", num(eventsX))
					event(&cards, label, color, eventsX, ey, eh)
					ey += eh + EVENT_GAP
				}
				if cards.Len() > 0 {
					svg.WriteStrings(&d, "M", num(axisX), ",", num(cy), "H", num(spineX), "V", num(last))
				}
				openPeriod(i)
				period(i, color, left, cy-h/2, h)
				svg.WriteStrings(&body, `<path class="pele-edge" d="`, d.String(), `"`, LINE, `/>`)
				dot(axisX, cy, color)
				svg.WriteStrings(&body, cards.String(), "</g>")
				y = math.Max(cy+h/2, ey-EVENT_GAP) + GAP

			}
			if len(group.periods) == 0 {
				y += GAP - SECTION_GAP - 4
			}
			if group.section >= 0 {
				body.WriteString("</g>")
			}
		}
		if len(groups) > 0 {
			y -= GAP
		}
		if drawn {
			tip := y + TAIL + svg.ARROW
			axis = `<path class="pele-axis" d="M` + num(axisX) + "," + num(top) + "V" + num(tip-svg.ARROW+1) + `" fill="none" stroke="var(--_l)"/>` +
				svg.Marker("arrow_point", axisX, tip, 0, 1, "var(--_l)")
			y = tip
		}
		height = y + pad
	} else {
		headerH := 0.0
		sectionLabels := make([]*text.Label, len(groups))
		for g, group := range groups {
			if group.section < 0 {
				continue
			}
			label := sectionLabel(group, float64(float64(max(len(group.periods), 1))*pitch)-GAP)
			headerH = math.Max(headerH, chipHeight(label))
			sectionLabels[g] = &label
			groupLabels[g] = label
		}
		chipH := 0.0
		for _, label := range periodLabels {
			chipH = math.Max(chipH, chipHeight(label))
		}
		periodsY := top
		if hasSections {
			periodsY += headerH + SECTION_GAP
		}
		axisY := periodsY + chipH
		if chipH > 0 {
			axisY += AXIS
		}
		eventsY := axisY + AXIS
		bottom := top + headerH
		if drawn {
			bottom = axisY + TAIL
		}
		slot := 0
		for g, group := range groups {
			span := max(len(group.periods), 1)
			gx := left + float64(float64(slot)*pitch)
			open(group)
			if label := sectionLabels[g]; label != nil {
				header(group, *label, gx, top, float64(float64(span)*pitch)-GAP, headerH)
			}
			for k, i := range group.periods {
				color := colorOf(group, i)
				x := gx + float64(float64(k)*pitch)
				cx := x + colW/2
				var d, cards strings.Builder
				end := axisY
				if len(eventLabels[i]) > 0 {
					end = eventsY
				}
				svg.WriteStrings(&d, "M", num(cx), ",", num(periodsY+chipH), "V", num(end))
				ey := eventsY
				for _, eventLabel := range eventLabels[i] {
					eh := eventHeight(eventLabel)
					if ey > eventsY {
						svg.WriteStrings(&d, "M", num(cx), ",", num(ey-EVENT_GAP), "V", num(ey))
					}
					event(&cards, eventLabel, color, x, ey, eh)
					ey += eh + EVENT_GAP
				}
				bottom = math.Max(bottom, ey-EVENT_GAP)
				openPeriod(i)
				period(i, color, x, periodsY, chipH)
				svg.WriteStrings(&body, `<path class="pele-edge" d="`, d.String(), `"`, LINE, `/>`)
				dot(cx, axisY, color)
				svg.WriteStrings(&body, cards.String(), "</g>")
			}
			if group.section >= 0 {
				body.WriteString("</g>")
			}
			slot += span
		}
		if drawn {
			tip := left + bodyW
			axis = `<path class="pele-axis" d="M` + num(left) + "," + num(axisY) + "H" + num(tip-svg.ARROW+1) + `" fill="none" stroke="var(--_l)"/>` +
				svg.Marker("arrow_point", tip, axisY, 1, 0, "var(--_l)")
		}
		height = bottom + pad
	}

	totalWidth := math.Ceil(width + float64(2*pad))
	totalHeight := math.Ceil(math.Max(height, pad*2))
	var geometry *types.Geometry
	if options.Geometry != nil {
		tl := &types.GeoTimeline{Title: title.Plain()}
		for g, group := range groups {
			tl.Groups = append(tl.Groups, types.GeoGroup{Label: groupLabels[g].Plain(), Periods: group.periods})
		}
		for i := range periods {
			p := types.GeoPeriod{Label: periodLabels[i].Plain()}
			for _, label := range eventLabels[i] {
				p.Events = append(p.Events, label.Plain())
			}
			tl.Periods = append(tl.Periods, p)
		}
		geometry = &types.Geometry{Width: totalWidth, Height: totalHeight, Timeline: tl}
	}
	return types.Rendered{
		Geometry: geometry,
		SVG: svg.SvgDocument("timeline", totalWidth, totalHeight, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr},
			svg.LabelSvg(title, totalWidth/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil), axis, body.String()),
		Width:  totalWidth,
		Height: totalHeight,
		Links:  []types.LinkInfo{},
	}
}
