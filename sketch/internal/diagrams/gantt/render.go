// Ported from pele src/diagrams/gantt/render.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// pele's `text` is textSetting here, as the package text is imported. Dates are read in
// options.Location, or in the host's zone when it is nil.

package gantt

import (
	"math"
	"slices"
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/lazyre"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
	"tuios.dev/sketch/internal/util"
)

const (
	WIDTH     = 800
	MIN_PLOT  = 240
	LABEL_GAP = 8
)

var (
	RE_BREAK       = lazyre.New(`(?i)<br[` + js.S + `]*/?>`)
	reNewlineSpace = lazyre.New(`\n[` + js.S + `]+`)
)

func ganttConfig(config types.Config, key string) any {
	conf, _ := config["gantt"].(map[string]any)
	return conf[key]
}

func setting(config types.Config, key string, min, max float64) (float64, bool) {
	value, ok := ganttConfig(config, key).(float64)
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return math.Min(max, math.Max(min, value)), true
}

// settingOr is `setting(config, key, min, max) ?? def`.
func settingOr(config types.Config, key string, min, max, def float64) float64 {
	if v, ok := setting(config, key, min, max); ok {
		return v
	}
	return def
}

func textSetting(config types.Config, key string) (string, bool) {
	value, ok := ganttConfig(config, key).(string)
	return value, ok
}

func flag(config types.Config, key string) bool {
	value, ok := ganttConfig(config, key).(bool)
	return ok && value
}

// A task label sits on one row, so only its first line is kept.
func oneLine(raw string, measurer text.TextMeasurer, size float64) text.Label {
	label := text.LayoutLabel(RE_BREAK.ReplaceAllString(raw, " "), false, measurer, size, 4000, 0)
	if len(label.Lines) <= 1 {
		return label
	}
	label.Lines = label.Lines[:1]
	label.Widths = label.Widths[:1]
	label.Width = label.Widths[0]
	label.Height = label.LineHeight
	return label
}

func isFinite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// Whether scheduling gave the task a start and an end. One that waits on a task that never resolves has neither.
func scheduled(task *GanttTask) bool {
	return isFinite(val(task.startTime)) && isFinite(val(task.endTime))
}

// Mermaid's compact mode: within a section, a task goes on the first row that is free when it starts.
func packRows(tasks []*GanttTask, offset int, rowOf map[*GanttTask]int) int {
	var sorted []*GanttTask
	for _, task := range tasks {
		if scheduled(task) {
			sorted = append(sorted, task)
		}
	}
	slices.SortStableFunc(sorted, func(a, b *GanttTask) int {
		d := a.startTime.T - b.startTime.T
		if d == 0 {
			return a.order - b.order
		}
		if d < 0 {
			return -1
		}
		return 1
	})
	var free []float64
	for _, task := range sorted {
		start := task.startTime.T
		row := 0
		for row < len(free) && start < free[row] {
			row++
		}
		if row == len(free) {
			free = append(free, 0)
		}
		free[row] = task.endTime.T
		rowOf[task] = offset + row
	}
	rows := len(free)
	for _, task := range tasks {
		if _, ok := rowOf[task]; !ok {
			rowOf[task] = offset + rows
			rows++
		}
	}
	return max(rows, 1)
}

type Bar struct {
	task   *GanttTask
	label  text.Label
	row    int
	x0     float64
	x1     float64
	labelX float64
	inside bool
}

type plot struct {
	left, plotWidth, start, scale float64
}

func (p *plot) x(t float64) float64 {
	if p.scale == 0 {
		return p.left + p.plotWidth/2
	}
	return p.left + float64((t-p.start)*p.scale)
}

func renderGantt(model *GanttDb, config types.Config, options types.RenderOptions) types.Rendered {
	num := svg.Num
	size := types.Or(options.FontSize, 16)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)
	now := clock(options.Now)
	loc := location(options.Location)

	labelSize := settingOr(config, "fontSize", 4, 72, js.Round(size*0.8125))
	sectionSize := settingOr(config, "sectionFontSize", 4, 72, labelSize)
	tickSize := js.Round(size * 0.75)
	barHeight := settingOr(config, "barHeight", 4, 400, js.Round(labelSize*1.85))
	barGap := settingOr(config, "barGap", 0, 400, js.Round(barHeight/4))
	rowHeight := barHeight + barGap
	sectionStyles := int(js.Round(settingOr(config, "numberSectionStyles", 1, 1000, 4)))
	compact := model.displayMode == "compact"
	topAxis := model.topAxis || flag(config, "topAxis")

	tasks := model.getTasks(now, loc)
	var rowTasks, verts []*GanttTask
	for _, task := range tasks {
		if !task.vert {
			rowTasks = append(rowTasks, task)
		} else if scheduled(task) {
			verts = append(verts, task)
		}
	}

	categories := map[string]int{}
	var categoryKeys []string
	for _, task := range rowTasks {
		if _, ok := categories[task.section]; !ok {
			categories[task.section] = len(categoryKeys)
			categoryKeys = append(categoryKeys, task.section)
		}
	}

	rowOf := map[*GanttTask]int{}
	var rowSection []string
	if compact {
		groups := map[string][]*GanttTask{}
		var groupKeys []string
		for _, task := range rowTasks {
			if _, ok := groups[task.section]; !ok {
				groupKeys = append(groupKeys, task.section)
			}
			groups[task.section] = append(groups[task.section], task)
		}
		for _, section := range groupKeys {
			rows := packRows(groups[section], len(rowSection), rowOf)
			for i := 0; i < rows; i++ {
				rowSection = append(rowSection, section)
			}
		}
	} else {
		for _, task := range rowTasks {
			rowOf[task] = len(rowSection)
			rowSection = append(rowSection, task.section)
		}
	}
	rowCount := len(rowSection)

	start := math.Inf(1)
	stop := math.Inf(-1)
	for _, task := range tasks {
		if !scheduled(task) {
			continue
		}
		start = math.Min(start, task.startTime.T)
		stop = math.Max(stop, task.endTime.T)
	}
	hasPlot := start <= stop

	title := text.LayoutLabel(model.title, false, measurer, size, 4000, text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 16
	}
	acc := svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr}

	if !hasPlot {
		width := math.Ceil(math.Max(title.Width, 1) + float64(2*pad))
		height := math.Ceil(math.Max(title.Height, 1) + float64(2*pad))
		content := svg.LabelSvg(title, width/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil)
		rendered := types.Rendered{SVG: svg.SvgDocument("gantt", width, height, size, options, acc, content), Width: width, Height: height, Links: []types.LinkInfo{}}
		if options.Geometry != nil {
			rendered.Geometry = &types.Geometry{Width: width, Height: height, Gantt: &types.GeoGantt{Title: title.Plain(), Today: math.NaN()}}
		}
		return rendered
	}

	sectionLabels := map[string]text.Label{}
	sectionWidth := 0.0
	for _, section := range categoryKeys {
		label := text.LayoutLabel(section, false, measurer, sectionSize, 180, 0)
		sectionLabels[section] = label
		sectionWidth = math.Max(sectionWidth, label.Width)
	}

	axisFormat := model.axisFormat
	if axisFormat == "" {
		if model.dateFormat == "D" {
			axisFormat = "%d"
		} else if v, ok := textSetting(config, "axisFormat"); ok {
			axisFormat = v
		} else {
			axisFormat = "%Y-%m-%d"
		}
	}
	tickLabel := func(t float64) string { return timeFormat(axisFormat, js.NewDate(t, loc)) }
	sampleWidth := math.Max(measurer.Width(tickLabel(start), tickSize, 0), measurer.Width(tickLabel(stop), tickSize, 0))

	leftDefault := sampleWidth / 2
	if sectionWidth > 0 {
		leftDefault = math.Max(sectionWidth+24, sampleWidth/2)
	}
	left := pad + settingOr(config, "leftPadding", 0, 2000, leftDefault)
	rightPadding := settingOr(config, "rightPadding", 0, 2000, math.Max(16, sampleWidth/2))
	target := common.CapWidth(options, settingOr(config, "useWidth", 100, 20000, WIDTH), 280, 0)
	plotWidth := math.Max(MIN_PLOT, target-left-rightPadding-pad)
	right := left + plotWidth
	scale := 0.0
	if stop > start {
		scale = plotWidth / (stop - start)
	}
	p := &plot{left, plotWidth, start, scale}

	tickHeight := js.Round(tickSize * 1.75)
	topPadding, hasTopPadding := setting(config, "topPadding", 0, 2000)
	gridStart, hasGridStart := setting(config, "gridLineStartPadding", 0, 2000)
	headHeight := pad + titleHeight
	if topAxis {
		headHeight += tickHeight
	}
	plotTop := math.Max(headHeight, topPadding)
	// Mermaid starts its grid lines above the first row by the difference between these two settings.
	// Here that only uses room a larger `topPadding` leaves free.
	gridLead := 0.0
	if hasTopPadding && hasGridStart {
		gridLead = math.Max(0, math.Min(topPadding-gridStart, plotTop-headHeight))
	}
	plotHeight := float64(float64(max(rowCount, 1)) * rowHeight)
	plotBottom := plotTop + plotHeight

	// Bars and where their labels go: inside when they fit, otherwise beside the bar on the side with room.
	bars := make([]Bar, 0, len(rowTasks))
	extent := right + rightPadding
	for _, task := range rowTasks {
		label := oneLine(task.task, measurer, labelSize)
		row := rowOf[task]
		if !scheduled(task) {
			labelX := left + LABEL_GAP + label.Width/2
			extent = math.Max(extent, labelX+label.Width/2)
			bars = append(bars, Bar{task, label, row, math.NaN(), math.NaN(), labelX, false})
			continue
		}
		x0 := p.x(task.startTime.T)
		minWidth := 2.0
		if task.milestone {
			minWidth = 0
		}
		end := task.endTime
		if task.renderEndTime != nil {
			end = task.renderEndTime
		}
		x1 := math.Max(p.x(end.T), x0+minWidth)
		if task.milestone {
			mid := x0 + (p.x(task.endTime.T)-x0)/2
			x0 = mid - barHeight/2
			x1 = mid + barHeight/2
		}
		inside := !task.milestone && label.Width+2*LABEL_GAP <= x1-x0
		labelX := (x0 + x1) / 2
		if !inside {
			fitsRight := x1+LABEL_GAP+label.Width <= right+rightPadding
			fitsLeft := x0-LABEL_GAP-label.Width >= left
			if fitsRight || !fitsLeft {
				labelX = x1 + LABEL_GAP + label.Width/2
			} else {
				labelX = x0 - LABEL_GAP - label.Width/2
			}
			extent = math.Max(extent, labelX+label.Width/2)
		}
		bars = append(bars, Bar{task, label, row, x0, x1, labelX, inside})
	}

	interval := model.tickInterval
	if interval == "" {
		interval, _ = textSetting(config, "tickInterval")
	}
	weekday := model.weekday
	if weekday == "" {
		if v, ok := textSetting(config, "weekday"); ok {
			weekday = v
		} else {
			weekday = "sunday"
		}
	}
	count := max(2, min(10, int(math.Floor(plotWidth/(sampleWidth+24)))))
	ticks, ok := intervalTicks(interval, weekday, start, stop, int(math.Ceil(plotWidth/3)), loc)
	if !ok {
		ticks = autoTicks(start, stop, count, loc)
	}

	var grid, axisBottom, axisTop strings.Builder
	lastRight := math.Inf(-1)
	lastLabel := ""
	bottomY := plotBottom + tickHeight/2 + 2
	topY := plotTop - gridLead - tickHeight/2 - 2
	for _, t := range ticks {
		tx := p.x(t)
		grid.WriteString("M" + num(tx) + "," + num(plotTop-gridLead) + "V" + num(plotBottom))
		label := tickLabel(t)
		width := measurer.Width(label, tickSize, 0)
		// A label is skipped when it would run into the one before it or only repeat it; its grid line stays.
		if tx-width/2 < lastRight+8 || label == lastLabel {
			continue
		}
		lastRight = tx + width/2
		lastLabel = label
		extent = math.Max(extent, lastRight)
		body := `text-anchor="middle">` + svg.Esc(label) + `</text>`
		axisBottom.WriteString(`<text x="` + num(tx) + `" y="` + num(bottomY+float64(tickSize*0.35)) + `" ` + body)
		if topAxis {
			axisTop.WriteString(`<text x="` + num(tx) + `" y="` + num(topY+float64(tickSize*0.35)) + `" ` + body)
		}
	}

	// Vertical markers take no row. Their labels go under the axis, on as many lines as it takes to keep them apart.
	var vertLines, vertLabels strings.Builder
	var vertEnds []float64
	vertTop := plotBottom + tickHeight + 4
	vertLine := js.Round(labelSize * 1.5)
	sortedVerts := slices.Clone(verts)
	slices.SortStableFunc(sortedVerts, func(a, b *GanttTask) int {
		d := a.startTime.T - b.startTime.T
		if d < 0 {
			return -1
		}
		if d > 0 {
			return 1
		}
		return 0
	})
	for _, task := range sortedVerts {
		vx := p.x(task.startTime.T)
		label := oneLine(task.task, measurer, labelSize)
		cx := math.Max(pad+label.Width/2, vx)
		line := 0
		for line < len(vertEnds) && cx-label.Width/2 < vertEnds[line]+8 {
			line++
		}
		if line == len(vertEnds) {
			vertEnds = append(vertEnds, 0)
		}
		vertEnds[line] = cx + label.Width/2
		extent = math.Max(extent, vertEnds[line])
		vertLines.WriteString(`<path class="pele-vert" data-id="` + svg.EscText(task.id) + `" d="M` + num(vx) + `,` + num(plotTop-gridLead) + `V` + num(plotBottom) + `"/>`)
		vertLabels.WriteString(svg.LabelSvg(label, cx, vertTop+float64(float64(line)*vertLine)+vertLine/2, ` class="pele-vert-label" data-id="`+svg.EscText(task.id)+`"`, nil))
	}

	width := math.Ceil(math.Max(extent, pad+title.Width) + pad)
	vertHeight := 0.0
	if len(vertEnds) > 0 {
		vertHeight = 4 + float64(float64(len(vertEnds))*vertLine)
	}
	height := math.Ceil(plotBottom + tickHeight + vertHeight + pad)

	// Section bands, one per run of rows that share a section, shaded on alternate sections.
	var bands, sectionOut strings.Builder
	for row := 0; row < rowCount; {
		section := rowSection[row]
		end := row + 1
		for end < rowCount && rowSection[end] == section {
			end++
		}
		index := categories[section] % sectionStyles
		y := plotTop + float64(float64(row)*rowHeight)
		h := float64(end-row) * rowHeight
		if len(categoryKeys) > 1 || section != "" {
			fill := "none"
			if index%2 == 0 {
				fill = "var(--_s)"
			}
			bands.WriteString(`<rect class="pele-section pele-section-` + js.Int(index) + `" x="0" y="` + num(y) + `" width="` + js.Num(width) + `" height="` + num(h) + `" fill="` + fill + `"/>`)
		}
		label := sectionLabels[section]
		sectionOut.WriteString(svg.LabelSvg(label, pad+8+label.Width/2, y+h/2, ` class="pele-section-label" data-id="`+svg.EscText(section)+`"`, options.Icons))
		row = end
	}

	// Excluded days, as Mermaid marks them: a day at a time from the first task's start, and not at all
	// when the chart spans more than five years.
	var excluded strings.Builder
	// The excluded spans as times, for the geometry.
	var excludedSpans [][2]float64
	if (len(model.excludes) > 0 || len(model.includes) > 0) && add(js.NewDate(start, loc), 6, "y").T > stop {
		var from, to *js.Date
		flush := func() {
			if from == nil || to == nil {
				return
			}
			dayStart := *from
			dayStart.SetHours(0, 0, 0, 0)
			dayEnd := *to
			dayEnd.SetHours(23, 59, 59, 999)
			ex0 := math.Max(left, p.x(dayStart.T))
			ex1 := math.Min(right, p.x(dayEnd.T))
			if ex1 > ex0 {
				if options.Geometry != nil {
					excludedSpans = append(excludedSpans, [2]float64{math.Max(start, dayStart.T), math.Min(stop, dayEnd.T)})
				}
				excluded.WriteString(`<rect x="` + num(ex0) + `" y="` + num(plotTop) + `" width="` + num(ex1-ex0) + `" height="` + num(plotHeight) + `"/>`)
			}
			from, to = nil, nil
		}
		for day := js.NewDate(start, loc); day.T <= stop; day = addDays(day, 1) {
			if model.isInvalidDate(day) {
				if from == nil {
					from = ptr(day)
				}
				to = ptr(day)
			} else {
				flush()
			}
		}
		flush()
	}

	links := []types.LinkInfo{}
	var tasksOut strings.Builder
	for _, bar := range bars {
		task := bar.task
		cy := plotTop + float64(float64(bar.row)*rowHeight) + rowHeight/2
		state := ""
		if task.active {
			state = "active"
		} else if task.done {
			state = "done"
		}
		classes := "pele-task"
		if state != "" {
			classes += " pele-" + state
		}
		if task.crit {
			classes += " pele-crit"
		}
		if task.milestone {
			classes += " pele-milestone"
		}
		if contains(task.classes, "clickable") {
			classes += " pele-clickable"
		}

		shape := ""
		textFill := ""
		if math.IsNaN(bar.x0) {
			classes += " pele-unscheduled"
			textFill = ` fill="var(--_m)"`
		} else {
			// A critical task takes the critical color in place of the accent. Done, it keeps the fill
			// of finished work and only its border is in that color.
			color := "var(--_c)"
			if task.crit {
				color = svg.CRITICAL
			}
			paint := `fill="` + color + `"`
			if state == "done" {
				paint = `fill="var(--_a)"`
			} else if state == "active" {
				paint = `fill="` + color + `" fill-opacity="0.25"`
			}
			strokeColor := color
			if state == "done" && !task.crit {
				strokeColor = "var(--_b)"
			}
			stroke := `stroke="` + strokeColor + `"`
			if task.milestone {
				cx := (bar.x0 + bar.x1) / 2
				r := barHeight / 2
				shape = `<path d="M` + num(cx) + `,` + num(cy-r) + `L` + num(cx+r) + `,` + num(cy) + `L` + num(cx) + `,` + num(cy+r) + `L` + num(cx-r) + `,` + num(cy) + `Z" ` + paint + ` ` + stroke + ` stroke-linejoin="round"/>`
			} else {
				shape = `<rect x="` + num(bar.x0) + `" y="` + num(cy-barHeight/2) + `" width="` + num(bar.x1-bar.x0) + `" height="` + num(barHeight) + `" ` + paint + ` ` + stroke + svg.ROUNDED + `/>`

			}
			if bar.inside {
				if state == "" {
					textFill = ` fill="var(--_bg)"`
				}
			} else if state == "done" {
				textFill = ` fill="var(--_m)"`
			}
		}

		body := shape + svg.LabelSvg(bar.label, bar.labelX, cy, ` class="pele-label"`+textFill, nil)
		if url, ok := model.links[task.id]; ok {
			body = util.Linked(body, url, task.id, &links, options, "")
		}
		tasksOut.WriteString(`<g class="` + classes + `" data-id="` + svg.EscText(task.id) + `">` + body + `</g>`)
	}

	today := ""
	if model.todayMarker != "off" && now >= start && now <= stop && stop > start {
		tx := p.x(now)
		top := plotTop - gridLead
		style := ""
		if model.todayMarker != "" {
			style = svg.ResolveStyle(strings.Split(model.todayMarker, ",")).Line
		}
		today = `<g class="pele-today"><path d="M` + num(tx) + `,` + num(top) + `V` + num(plotBottom) + `" fill="none" stroke="var(--_c)" stroke-width="2"` + style + `/>`
		if style == "" {
			today += `<path d="M` + num(tx-4) + `,` + num(top-5) + `H` + num(tx+4) + `L` + num(tx) + `,` + num(top+1) + `Z" fill="var(--_c)"/>`
		}
		today += "</g>"
	}

	axisLine := "M" + num(left) + "," + num(plotBottom) + "H" + num(right)
	if topAxis {
		axisLine += "M" + num(left) + "," + num(plotTop-gridLead) + "H" + num(right)
	}
	content := make([]string, 0, 32)
	content = append(content, svg.LabelSvg(title, width/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil))
	if bands.Len() > 0 {
		content = append(content, `<g class="pele-sections">`, bands.String(), `</g>`)
	}
	if excluded.Len() > 0 {
		content = append(content, `<g class="pele-excluded" fill="var(--_a)">`, excluded.String(), `</g>`)
	}
	// The grid's path data sits inside a tag, so it joins the strings around it.
	content = append(content, `<g class="pele-grid" fill="none"><path d="`+grid.String()+`" stroke="var(--_b)" stroke-dasharray="1 3"/><path d="`+axisLine+`" stroke="var(--_b)"/></g>`)
	if vertLines.Len() > 0 {
		content = append(content, `<g class="pele-verts" fill="none" stroke="var(--_l)" stroke-width="1.5" stroke-dasharray="4 3">`, vertLines.String(), `</g>`)
	}
	content = append(content, today)
	content = append(content, `<g class="pele-tasks" font-size="`+num(labelSize)+`">`, tasksOut.String(), `</g>`)
	content = append(content, `<g class="pele-section-labels" font-size="`+num(sectionSize)+`">`, sectionOut.String(), `</g>`)
	content = append(content, `<g class="pele-axis" font-size="`+js.Num(tickSize)+`" fill="var(--_m)">`, axisBottom.String(), axisTop.String(), `</g>`)
	if vertLabels.Len() > 0 {
		content = append(content, `<g class="pele-vert-labels" font-size="`+num(labelSize)+`">`, vertLabels.String(), `</g>`)
	}

	rendered := types.Rendered{SVG: svg.SvgDocument("gantt", width, height, size, options, acc, content...), Width: width, Height: height, Links: links}
	if options.Geometry != nil {
		rendered.Geometry = ganttGeometry(title, start, stop, rowSection, bars, ticks, tickLabel, sortedVerts, now, model.todayMarker != "off", width, height)
		rendered.Geometry.Gantt.Excluded = excludedSpans
	}
	return rendered
}

// ganttGeometry is the chart as lists, for drawing it some other way.
func ganttGeometry(title text.Label, start, stop float64, rows []string, bars []Bar, ticks []float64, tickLabel func(float64) string,
	verts []*GanttTask, now float64, today bool, width, height float64) *types.Geometry {
	g := &types.GeoGantt{Title: title.Plain(), Start: start, Stop: stop, Today: math.NaN()}
	for _, row := range rows {
		g.Rows = append(g.Rows, text.DecodeEntities(row))
	}
	for _, bar := range bars {
		task := bar.task
		t := types.GeoTask{Label: strings.Join(bar.label.Plain(), " "), Row: bar.row, Scheduled: scheduled(task),
			Milestone: task.milestone, Crit: task.crit, Done: task.done, Active: task.active}
		if t.Scheduled {
			end := task.endTime
			// A milestone sits where the SVG puts it, at the middle of its own
			// times; a bar runs to where excluded days pushed its end.
			if task.renderEndTime != nil && !task.milestone {
				end = task.renderEndTime
			}
			t.Start, t.End = task.startTime.T, end.T
		}
		g.Tasks = append(g.Tasks, t)
	}
	for _, t := range ticks {
		g.Ticks = append(g.Ticks, types.GeoTick{T: t, Label: tickLabel(t)})
	}
	for _, task := range verts {
		g.Verts = append(g.Verts, types.GeoTick{T: task.startTime.T, Label: text.DecodeEntities(RE_BREAK.ReplaceAllString(task.task, " "))})
	}
	if today && now >= start && now <= stop && stop > start {
		g.Today = now
	}
	return &types.Geometry{Width: width, Height: height, Gantt: g}
}
