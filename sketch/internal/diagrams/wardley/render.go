// Ported from pele src/diagrams/wardley/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package wardley

import (
	"math"
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

var DEFAULT_STAGES = []string{"Genesis", "Custom Built", "Product", "Commodity"}

const (
	// CANVAS_MARGIN is the room Mermaid's canvas has around the plot on every side.
	CANVAS_MARGIN = 48
	R             = 6
	SQUARE        = 10
	LABEL_OFFSET  = 8
	PIPELINE_PAD  = 15
	FORCE_WIDTH   = 60
	FORCE_HEIGHT  = 30
	FORCE_HEAD    = 20
	ACCENT        = "var(--_c)"
	LINE          = "var(--_l)"
)

type Placed struct {
	x, y float64
	node *WardleyNode
}

func section(config types.Config) map[string]any {
	s, _ := config["wardley-beta"].(map[string]any)
	return s
}

func setting(config types.Config, key string, fallback, min, max float64) float64 {
	if value, ok := section(config)[key].(float64); ok && value >= min && value <= max {
		return value
	}
	return fallback
}

func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// startLabel writes a label with its lines starting at x and its first baseline at y.
func startLabel(label text.Label, x, y float64, attrs string) string {
	shared := svg.SharedStyle(label)
	var body strings.Builder
	baseline := y
	for _, line := range label.Lines {
		first := true
		for _, span := range line {
			if span.Icon != "" {
				continue
			}
			body.WriteString("<tspan")
			if first {
				svg.WriteStrings(&body, ` x="`, svg.Num(x), `" y="`, svg.Num(baseline), `"`)
			}
			svg.WriteStrings(&body, svg.SpanStyle(span.Style&^shared), ">", svg.Esc(span.Text), "</tspan>")
			first = false
		}
		baseline += label.LineHeight
	}
	if body.Len() == 0 {
		return ""
	}
	return "<text" + svg.TextStyle(attrs, shared) + ` xml:space="preserve">` + body.String() + "</text>"
}

func dimension(config types.Config, given *float64, key string, fallback float64) float64 {
	value := 0.0
	if given != nil && !math.IsNaN(*given) && !math.IsInf(*given, 0) {
		value = *given
	} else {
		value = setting(config, key, fallback, 1, 1e6)
	}
	return clamp(value, 200, 10000) - 2*CANVAS_MARGIN
}

func sizeOf(model *WardleyModel, k int) *float64 {
	if model.size == nil {
		return nil
	}
	v := model.size[k]
	return &v
}

func renderWardley(model *WardleyModel, config types.Config, options types.RenderOptions) types.Rendered {
	natural := dimension(config, sizeOf(model, 0), "width", 900)
	return common.FitWidth(options, natural, 160, func(plotW float64, tight bool) types.Rendered {
		return draw(model, config, options, plotW, natural, tight)
	})
}

func draw(model *WardleyModel, config types.Config, options types.RenderOptions, plotW, naturalW float64, tight bool) types.Rendered {
	size := types.Or(options.FontSize, 16)
	small := js.Round(float64(size * 0.8125))
	tiny := js.Round(float64(size * 0.6875))
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)

	// A narrower map is not as much shorter, so that components keep some room between them.
	plotH := float64(dimension(config, sizeOf(model, 1), "height", 600) * math.Max(0.75, plotW/naturalW))
	showGrid := section(config)["showGrid"] == true

	minX, minY, maxX, maxY := 0.0, 0.0, plotW, plotH
	grow := func(x0, y0, x1, y1 float64) {
		if x0 < minX {
			minX = x0
		}
		if y0 < minY {
			minY = y0
		}
		if x1 > maxX {
			maxX = x1
		}
		if y1 > maxY {
			maxY = y1
		}
	}
	centered := func(label text.Label, cx, cy float64, attrs string) string {
		grow(cx-label.Width/2, cy-label.Height/2, cx+label.Width/2, cy+label.Height/2)
		return svg.LabelSvg(label, cx, cy, attrs, options.Icons)
	}
	starting := func(label text.Label, x, y float64, attrs string) string {
		grow(x, y-label.Size, x+label.Width, y-label.Size+label.Height+4)
		return startLabel(label, x, y, attrs)
	}
	defaultWidth := 4000.0
	if tight {
		defaultWidth = math.Max(64, math.Min(120, plotW/2))
	}
	textAt := func(raw string, fontSize, maxWidth float64, style int) text.Label {
		return text.LayoutLabel(raw, false, measurer, fontSize, maxWidth, style)
	}
	projectX := func(value float64) float64 { return float64((value / 100) * plotW) }
	projectY := func(value float64) float64 { return plotH - float64((value/100)*plotH) }

	stages := model.axes.stages
	if len(stages) == 0 {
		stages = DEFAULT_STAGES
	}
	boundaries := model.axes.stageBoundaries
	custom := model.axes.hasBoundaries && len(boundaries) == len(stages)
	var frame, stageLabels strings.Builder
	stageBottom := plotH
	previous := 0.0
	smallText, tinyText := js.Num(small), js.Num(tiny)
	n := float64(len(stages))
	for index, stage := range stages {
		start := float64(index) / n
		end := float64(index+1) / n
		if custom {
			start = previous
			end = clamp(boundaries[index], 0, 1)
		}
		previous = end
		if index > 0 {
			svg.WriteStrings(&frame, `<path class="pele-wardley-stage" d="M`, svg.Num(float64(start*plotW)), ",0V", svg.Num(plotH), `" stroke="var(--_b)" stroke-dasharray="4 4"/>`)
		}
		label := textAt(stage, small, math.Max(48, float64(math.Abs(end-start)*plotW)-12), 0)
		stageLabels.WriteString(centered(label, float64(((start+end)/2)*plotW), plotH+8+label.Height/2, ` class="pele-wardley-stage-label" font-size="`+smallText+`" fill="var(--_m)"`))
		stageBottom = math.Max(stageBottom, plotH+8+label.Height)
	}
	if showGrid {
		for i := 1; i < 4; i++ {
			svg.WriteStrings(&frame, `<path class="pele-wardley-grid" d="M`, svg.Num(float64(plotW*float64(i))/4), ",0V", svg.Num(plotH), "M0,", svg.Num(float64(plotH*float64(i))/4), "H", svg.Num(plotW), `" stroke="var(--_b)" stroke-dasharray="2 6"/>`)
		}
	}
	svg.WriteStrings(&frame, `<path class="pele-wardley-axis" d="M0,0V`, svg.Num(plotH), "H", svg.Num(plotW), `" stroke="`, LINE, `"/>`)
	xTitle := textAt("Evolution", small, defaultWidth, 0)
	yTitle := textAt("Visibility", small, defaultWidth, 0)
	axisAttrs := ` class="pele-wardley-axis-label" font-size="` + smallText + `" font-weight="var(--_hw)"`
	axisLabels := centered(xTitle, plotW/2, stageBottom+6+xTitle.Height/2, axisAttrs) +
		svg.LabelSvg(yTitle, -16, plotH/2, axisAttrs+` transform="rotate(-90 -16 `+svg.Num(plotH/2)+`)"`, options.Icons)
	grow(-16-yTitle.Height/2, 0, 0, 0)
	legendTop := stageBottom + 6 + xTitle.Height + 10

	positions := make([]*Placed, len(model.nodes))
	positionOf := make(map[string]*Placed, len(model.nodes))
	for i, node := range model.nodes {
		positions[i] = &Placed{x: projectX(node.x), y: projectY(node.y), node: node}
		positionOf[node.id] = positions[i]
	}

	// Pipelines: a box around the components, with the parent as a square on its top edge.
	var pipelines strings.Builder
	members := map[string]map[string]bool{}
	for _, pipeline := range model.pipelines {
		set := map[string]bool{}
		for _, id := range pipeline.componentIDs {
			set[id] = true
		}
		members[pipeline.nodeID] = set
		var parts []*Placed
		for _, id := range pipeline.componentIDs {
			if part := positionOf[id]; part != nil {
				parts = append(parts, part)
			}
		}
		if len(parts) == 0 {
			continue
		}
		js.Sort(parts, func(a, b *Placed) float64 { return a.x - b.x })
		left := parts[0].x
		right := parts[len(parts)-1].x
		y := parts[0].y
		top := y - 2*R
		if parent := positionOf[pipeline.nodeID]; parent != nil {
			parent.x = (left + right) / 2
			parent.y = top - float64(SQUARE)/6
		}
		grow(left-PIPELINE_PAD, top, right+PIPELINE_PAD, top+4*R)
		svg.WriteStrings(&pipelines, `<g class="pele-wardley-pipeline" data-id="`, svg.EscText(pipeline.nodeID), `">`,
			`<rect x="`, svg.Num(left-PIPELINE_PAD), `" y="`, svg.Num(top), `" width="`, svg.Num(right-left+2*PIPELINE_PAD), `" height="`, svg.Num(4*R),
			`" fill="var(--_s)" stroke="var(--_b)"`, svg.ROUNDED, `/>`)
		if len(parts) > 1 {
			svg.WriteStrings(&pipelines, `<path d="M`, svg.Num(left), ",", svg.Num(y), "H", svg.Num(right), `" stroke="`, LINE, `" stroke-dasharray="4 4"/>`)
		}
		pipelines.WriteString("</g>")
	}

	// How far from a node's center a line to it stops.
	anchorLabels := map[*WardleyNode]text.Label{}
	anchorWidth := 4000.0
	if tight {
		anchorWidth = math.Max(64, plotW/2)
	}
	reach := func(at *Placed, ux, uy float64) float64 {
		node := at.node
		if node.className == "anchor" {
			label, ok := anchorLabels[node]
			if !ok {
				label = textAt(node.label, small, anchorWidth, text.Bold)
				anchorLabels[node] = label
			}
			return 1 / js.Hypot(ux/(label.Width/2+5), uy/float64(small*0.8))
		}
		if node.isPipelineParent {
			return SQUARE / math.Sqrt2
		}
		if node.sourceStrategy != "" {
			return 2 * R
		}
		return R
	}

	// Quadrants around a node that a link runs through: 1 up and right, 2 down and right, 4 up and left, 8 down and left.
	taken := map[*Placed]int{}
	occupy := func(at *Placed, ux, uy float64) {
		if math.Abs(ux) < 0.25 || math.Abs(uy) < 0.25 {
			return
		}
		quadrant := 8
		switch {
		case ux > 0 && uy < 0:
			quadrant = 1
		case ux > 0:
			quadrant = 2
		case uy < 0:
			quadrant = 4
		}
		taken[at] |= quadrant
	}

	trim := svg.MarkerTrim("arrow_point")
	var links strings.Builder
	for _, link := range model.links {
		from := positionOf[link.source]
		to := positionOf[link.target]
		if from == nil || to == nil || from == to {
			continue
		}
		// A pipeline component belongs to its parent already.
		if members[link.target][link.source] {
			continue
		}
		dx := to.x - from.x
		dy := to.y - from.y
		distance := js.Hypot(dx, dy)
		if distance < 0.01 {
			continue
		}
		ux := dx / distance
		uy := dy / distance
		start := reach(from, ux, uy)
		end := reach(to, ux, uy)
		if start+end >= distance {
			continue
		}
		occupy(from, ux, uy)
		occupy(to, -ux, -uy)
		x1 := from.x + float64(ux*start)
		y1 := from.y + float64(uy*start)
		x2 := to.x - float64(ux*end)
		y2 := to.y - float64(uy*end)
		var heads strings.Builder
		if link.flow == "forward" || link.flow == "bidirectional" {
			svg.WriteMarker(&heads, "arrow_point", x2, y2, ux, uy, LINE)
			x2 -= float64(ux * trim)
			y2 -= float64(uy * trim)
		}
		if link.flow == "backward" || link.flow == "bidirectional" {
			svg.WriteMarker(&heads, "arrow_point", x1, y1, -ux, -uy, LINE)
			x1 += float64(ux * trim)
			y1 += float64(uy * trim)
		}
		caption := ""
		if link.label != "" {
			label := textAt(link.label, tiny+1, 200, 0)
			// Beside the middle of the line, on its upper side.
			side := -1.0
			if ux >= 0 {
				side = 1
			}
			px := float64(uy * side)
			py := float64(-ux * side)
			away := 5 + float64(math.Abs(px)*label.Width)/2 + float64(math.Abs(py)*label.Height)/2
			caption = centered(label, (from.x+to.x)/2+float64(px*away), (from.y+to.y)/2+float64(py*away), ` class="pele-edge-label" font-size="`+js.Num(tiny+1)+`" fill="var(--_m)"`)
		}
		dashedClass, dashedAttr := "", ""
		if link.dashed {
			dashedClass, dashedAttr = " pele-wardley-dashed", ` stroke-dasharray="6 6"`
		}
		svg.WriteStrings(&links, `<g class="pele-edge`, dashedClass, `" data-id="`, svg.EscText(link.source), "-", svg.EscText(link.target), `">`,
			`<path d="M`, svg.Num(x1), ",", svg.Num(y1), "L", svg.Num(x2), ",", svg.Num(y2), `" stroke="`, LINE, `"`, dashedAttr, `/>`,
			heads.String(), caption, "</g>")
	}

	// Evolution: a dashed arrow to where the component is heading, drawn as an open circle.
	var trends strings.Builder
	for _, trend := range model.trends {
		origin := positionOf[trend.nodeID]
		if origin == nil {
			continue
		}
		tx := projectX(trend.targetX)
		ty := projectY(trend.targetY)
		dx := tx - origin.x
		dy := ty - origin.y
		distance := js.Hypot(dx, dy)
		arrow := ""
		start := 0.0
		if distance > 0 {
			start = reach(origin, dx/distance, dy/distance)
		}
		if distance > start+R+4 {
			ux := dx / distance
			uy := dy / distance
			ex := tx - float64(ux*(R+2))
			ey := ty - float64(uy*(R+2))
			var b strings.Builder
			svg.WriteStrings(&b, `<path d="M`, svg.Num(origin.x+float64(ux*start)), ",", svg.Num(origin.y+float64(uy*start)), "L", svg.Num(ex-float64(ux*trim)), ",", svg.Num(ey-float64(uy*trim)),
				`" stroke="`, ACCENT, `" stroke-dasharray="4 4"/>`)
			svg.WriteMarker(&b, "arrow_point", ex, ey, ux, uy, ACCENT)
			arrow = b.String()
		}
		grow(tx-R, ty-R, tx+R, ty+R)
		svg.WriteStrings(&trends, `<g class="pele-wardley-trend" data-id="`, svg.EscText(trend.nodeID), `">`, arrow,
			`<circle cx="`, svg.Num(tx), `" cy="`, svg.Num(ty), `" r="`, js.Num(R), `" fill="var(--_bg)" stroke="`, ACCENT, `"/></g>`)
	}

	var nodes strings.Builder
	tri := float64(1.2 * R)
	dot := float64(0.7 * R)
	for _, at := range positions {
		node := at.node
		x, y := at.x, at.y
		anchor := node.className == "anchor"
		strategy := node.sourceStrategy
		var shape strings.Builder
		if strategy != "" {
			fill := "var(--_bg)"
			switch strategy {
			case "outsource":
				fill = "var(--_m)"
			case "buy":
				fill = "var(--_a)"
			case "build":
				fill = "var(--_s)"
			}
			svg.WriteStrings(&shape, `<circle class="pele-wardley-`, strategy, `" cx="`, svg.Num(x), `" cy="`, svg.Num(y), `" r="`, js.Num(2*R), `" fill="`, fill, `" stroke="`, LINE, `"/>`)
		}
		if strategy == "market" {
			bx := float64(tri * js.Cos(math.Pi/6))
			by := y + float64(tri*js.Sin(math.Pi/6))
			svg.WriteStrings(&shape, `<path d="M`, svg.Num(x), ",", svg.Num(y-tri), "L", svg.Num(x-bx), ",", svg.Num(by), "H", svg.Num(x+bx), `Z" fill="none" stroke="`, LINE, `"/>`)
			for _, c := range [3][2]float64{{x, y - tri}, {x - bx, by}, {x + bx, by}} {
				svg.WriteStrings(&shape, `<circle cx="`, svg.Num(c[0]), `" cy="`, svg.Num(c[1]), `" r="`, svg.Num(dot), `" fill="var(--_bg)" stroke="`, LINE, `"/>`)
			}
		} else if node.isPipelineParent {
			svg.WriteStrings(&shape, `<rect x="`, svg.Num(x-SQUARE/2), `" y="`, svg.Num(y-SQUARE/2), `" width="`, js.Num(SQUARE), `" height="`, js.Num(SQUARE), `" fill="var(--_bg)" stroke="`, LINE, `"/>`)
		} else if !anchor {
			svg.WriteStrings(&shape, `<circle cx="`, svg.Num(x), `" cy="`, svg.Num(y), `" r="`, js.Num(R), `" fill="var(--_bg)" stroke="`, LINE, `"/>`)
		}
		if node.inertia {
			offset := float64(R) + 10
			if node.isPipelineParent {
				offset = float64(SQUARE)/2 + 10
			}
			if strategy != "" {
				offset += R + 4
			}
			svg.WriteStrings(&shape, `<rect class="pele-wardley-inertia" x="`, svg.Num(x+offset), `" y="`, svg.Num(y-R), `" width="4" height="`, js.Num(2*R), `" fill="`, LINE, `"/>`)
		}
		var ox, oy *float64
		if node.labelOffsetX != nil {
			v := clamp(*node.labelOffsetX, -5000, 5000)
			ox = &v
		}
		if node.labelOffsetY != nil {
			v := clamp(*node.labelOffsetY, -5000, 5000)
			oy = &v
		}
		var caption string
		if anchor {
			label, ok := anchorLabels[node]
			if !ok {
				label = textAt(node.label, small, anchorWidth, text.Bold)
			}
			caption = centered(label, x+types.Or(ox, 0), y+types.Or(oy, -3), ` class="pele-label" font-size="`+smallText+`" font-weight="var(--_hw)"`)
		} else {
			label := textAt(node.label, small, defaultWidth, 0)
			attrs := ` class="pele-label" font-size="` + smallText + `"`
			lift := float64(LABEL_OFFSET)
			if strategy != "" {
				lift += 10
			}
			if ox != nil || oy != nil {
				caption = starting(label, x+types.Or(ox, lift), y+types.Or(oy, -lift), attrs)
			} else if node.inPipeline {
				caption = centered(label, x, y+2*R+4+label.Height/2, attrs)
			} else {
				// The label goes up and to the right unless a link leaves that way; then the next free corner.
				busy := taken[at]
				corner := 1
				switch {
				case busy&1 == 0:
					corner = 1
				case busy&2 == 0:
					corner = 2
				case busy&4 == 0:
					corner = 4
				case busy&8 == 0:
					corner = 8
				}
				right := corner == 1 || corner == 2
				up := corner == 1 || corner == 4
				lx := x - lift - label.Width
				if right {
					lx = x + lift
				}
				ly := y + lift + float64(small*0.75)
				if up {
					ly = y - lift
				}
				caption = starting(label, lx, ly, attrs)
			}
		}
		grow(x-2*R, y-2*R, x+2*R+20, y+2*R)
		kind := "component"
		switch {
		case anchor:
			kind = "anchor"
		case node.isPipelineParent:
			kind = "pipeline-parent"
		case node.inPipeline:
			kind = "pipeline-component"
		}
		svg.WriteStrings(&nodes, `<g class="pele-node pele-wardley-`, kind, `" data-id="`, svg.EscText(node.id), `">`, shape.String(), caption, "</g>")
	}

	var annotations strings.Builder
	for _, annotation := range model.annotations {
		label := textAt(js.Num(annotation.number), tiny, 4000, text.Bold)
		svg.WriteStrings(&annotations, `<g class="pele-wardley-annotation" data-id="`, svg.EscText(js.Num(annotation.number)), `">`)
		for _, coordinate := range annotation.coordinates {
			x := projectX(coordinate[0])
			y := projectY(coordinate[1])
			r := math.Max(10, label.Width/2+4)
			grow(x-r, y-r, x+r, y+r)
			svg.WriteStrings(&annotations, `<circle cx="`, svg.Num(x), `" cy="`, svg.Num(y), `" r="`, svg.Num(r), `" fill="var(--_bg)" stroke="`, LINE, `"/>`,
				svg.LabelSvg(label, x, y, ` font-size="`+tinyText+`" font-weight="var(--_hw)"`, options.Icons))
		}
		annotations.WriteString("</g>")
	}
	var listed []WardleyAnnotation
	for _, a := range model.annotations {
		if a.text != "" {
			listed = append(listed, a)
		}
	}
	js.Sort(listed, func(a, b WardleyAnnotation) float64 { return a.number - b.number })
	if len(listed) > 0 {
		listWidth := 320.0
		if tight {
			listWidth = math.Max(80, plotW-20)
		}
		lines := make([]text.Label, len(listed))
		width, height := 0.0, 0.0
		for i, a := range listed {
			lines[i] = textAt(js.Num(a.number)+". "+a.text, small, listWidth, 0)
			width = math.Max(width, lines[i].Width)
			height += lines[i].Height
		}
		width += 20
		height += 14
		// Without a position the list goes under the map, where it covers nothing.
		bx, by := 0.0, legendTop
		if box := model.annotationsBox; box != nil {
			bx = clamp(projectX(box[0]), 0, math.Max(0, plotW-width))
			by = clamp(projectY(box[1]), 0, math.Max(0, plotH-height))
		}
		grow(bx, by, bx+width, by+height)
		svg.WriteStrings(&annotations, `<g class="pele-wardley-annotations"><rect x="`, svg.Num(bx), `" y="`, svg.Num(by), `" width="`, svg.Num(width), `" height="`, svg.Num(height),
			`" fill="var(--_bg)" stroke="var(--_b)"`, svg.ROUNDED, `/>`)
		y := by + 7
		for _, line := range lines {
			annotations.WriteString(startLabel(line, bx+10, y+line.LineHeight/2+float64(small*0.35), ` font-size="`+smallText+`"`))
			y += line.Height
		}
		annotations.WriteString("</g>")
	}

	var notes strings.Builder
	for _, note := range model.notes {
		notes.WriteString(starting(textAt(note.text, small, math.Min(360, float64(plotW*0.6)), text.Bold), projectX(note.x), projectY(note.y),
			` class="pele-wardley-note" font-size="`+smallText+`" font-weight="var(--_hw)"`))
	}

	// Accelerators point right, deaccelerators left.
	var forces strings.Builder
	force := func(name string, px, py float64, forward bool) {
		x := projectX(px)
		y := projectY(py)
		half := float64(FORCE_HEIGHT) / 2
		tip, tail, neck := x, x+FORCE_WIDTH, x+FORCE_HEAD
		if forward {
			tip, tail, neck = x+FORCE_WIDTH, x, x+FORCE_WIDTH-FORCE_HEAD
		}
		grow(x, y-half-8, x+FORCE_WIDTH, y+half+8)
		kind := "deaccelerator"
		if forward {
			kind = "accelerator"
		}
		svg.WriteStrings(&forces, `<g class="pele-wardley-`, kind, `" data-id="`, svg.EscText(name), `">`,
			`<path d="M`, svg.Num(tail), ",", svg.Num(y-half), "H", svg.Num(neck), "V", svg.Num(y-half-8), "L", svg.Num(tip), ",", svg.Num(y), "L", svg.Num(neck), ",", svg.Num(y+half+8),
			"V", svg.Num(y+half), "H", svg.Num(tail), `Z" fill="var(--_s)" stroke="`, LINE, `"/>`,
			centered(textAt(name, tiny+1, 160, text.Bold), x+FORCE_WIDTH/2, y+half+8+12, ` font-size="`+js.Num(tiny+1)+`" font-weight="var(--_hw)"`),
			"</g>")
	}
	for _, a := range model.accelerators {
		force(a.name, a.x, a.y, true)
	}
	for _, d := range model.deaccelerators {
		force(d.name, d.x, d.y, false)
	}

	titleWidth := 4000.0
	if tight {
		titleWidth = math.Max(80, plotW)
	}
	title := text.LayoutLabel(model.title, false, measurer, size, titleWidth, text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 12
	}
	left := math.Min(minX, plotW/2-title.Width/2)
	right := math.Max(maxX, plotW/2+title.Width/2)
	tx := pad - left
	ty := pad + titleHeight - minY
	totalWidth := math.Ceil(right - left + float64(2*pad))
	totalHeight := math.Ceil(maxY - minY + titleHeight + float64(2*pad))
	var content strings.Builder
	svg.WriteStrings(&content, svg.LabelSvg(title, tx+plotW/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, options.Icons),
		`<g transform="translate(`, svg.Num(tx), ",", svg.Num(ty), `)">`,
		`<g class="pele-wardley-frame" fill="none">`, frame.String(), `</g>`,
		stageLabels.String(), axisLabels, pipelines.String())
	if links.Len() > 0 {
		svg.WriteStrings(&content, `<g class="pele-edges" fill="none">`, links.String(), `</g>`)
	}
	if trends.Len() > 0 {
		svg.WriteStrings(&content, `<g class="pele-wardley-trends" fill="none">`, trends.String(), `</g>`)
	}
	svg.WriteStrings(&content, nodes.String(), annotations.String(), notes.String(), forces.String(), "</g>")

	rendered := types.Rendered{
		SVG:    svg.SvgDocument("wardley", totalWidth, totalHeight, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr}, content.String()),
		Width:  totalWidth,
		Height: totalHeight,
		Links:  []types.LinkInfo{},
	}
	if options.Geometry != nil {
		rendered.Geometry = &types.Geometry{Width: totalWidth, Height: totalHeight, Wardley: geometry(model, title, stages)}
	}
	return rendered
}

// geometry is the map as lists, for a drawing in text. Not part of pele.
func geometry(model *WardleyModel, title text.Label, stages []string) *types.GeoWardley {
	geo := &types.GeoWardley{Title: title.Plain(), Stages: stages}
	if model.axes.hasBoundaries && len(model.axes.stageBoundaries) == len(stages) {
		geo.Boundaries = model.axes.stageBoundaries
	}
	index := map[string]int{}
	for i, node := range model.nodes {
		index[node.id] = i
		kind := "component"
		switch {
		case node.className == "anchor":
			kind = "anchor"
		case node.isPipelineParent:
			kind = "pipeline"
		case node.inPipeline:
			kind = "pipeline-component"
		}
		geo.Nodes = append(geo.Nodes, types.GeoWardleyNode{Label: node.label, Evolution: node.x, Visibility: node.y, Kind: kind, Strategy: node.sourceStrategy, Inertia: node.inertia})
	}
	// A pipeline's parent sits over the middle of its components, as the SVG draws it.
	for _, pipeline := range model.pipelines {
		i, ok := index[pipeline.nodeID]
		if !ok {
			continue
		}
		lo, hi, found := 0.0, 0.0, false
		for _, id := range pipeline.componentIDs {
			if k, ok := index[id]; ok {
				x := model.nodes[k].x
				if !found || x < lo {
					lo = x
				}
				if !found || x > hi {
					hi = x
				}
				found = true
			}
		}
		if found {
			geo.Nodes[i].Evolution = (lo + hi) / 2
		}
	}
	for _, link := range model.links {
		from, okF := index[link.source]
		to, okT := index[link.target]
		if okF && okT && from != to {
			geo.Links = append(geo.Links, types.GeoWardleyLink{From: from, To: to, Label: link.label, Dashed: link.dashed, Flow: link.flow})
		}
	}
	for _, trend := range model.trends {
		if i, ok := index[trend.nodeID]; ok {
			geo.Trends = append(geo.Trends, [2]float64{float64(i), trend.targetX})
		}
	}
	for _, note := range model.notes {
		geo.Notes = append(geo.Notes, note.text)
	}
	for _, a := range model.annotations {
		if a.text != "" {
			geo.Notes = append(geo.Notes, js.Num(a.number)+". "+a.text)
		}
	}
	return geo
}
