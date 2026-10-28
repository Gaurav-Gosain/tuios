// Ported from pele src/diagrams/c4/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package c4

import (
	"math"
	"strconv"
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
	"tuios.dev/sketch/internal/util"
)

const PAIR_SHIFT = 7
const NARROW_GAP = 36
const WIDEN = 24
const MAX_OFFSET = 2000

// EDGE: a label may cover this much of a shape's edge, where there is no text.
const EDGE = 8

// ARROW is the length of an arrowhead, as in svg/edges.ts.
const ARROW = 8

// SLIDE: places along a relation to try for its label, nearest the middle first.
var SLIDE = [...]float64{0.5, 0.4, 0.6, 0.3, 0.7, 0.2, 0.8}

// CARE_BUDGET: above this many box comparisons, labels stay at the middle and lines are not steered around shapes.
const CARE_BUDGET = 2000000

func userStyle(fill, stroke, color string) svg.ResolvedStyle {
	var declarations []string
	if fill != "" {
		declarations = append(declarations, "fill:"+fill)
	}
	if stroke != "" {
		declarations = append(declarations, "stroke:"+stroke)
	}
	if color != "" {
		declarations = append(declarations, "color:"+color)
	}
	return svg.ResolveStyle(declarations)
}

func link(body, url, id string, links *[]types.LinkInfo, options types.RenderOptions) string {
	if url == "" {
		return body
	}
	// Entity encoding has hidden any `#name;` in the address. Put it back, so that the check sees what a browser would.
	if strings.Contains(url, "¶ß") {
		url = strings.ReplaceAll(url, "ﬂ°°", "#")
		url = strings.ReplaceAll(url, "ﬂ°", "#")
		url = strings.ReplaceAll(url, "¶ß", ";")
	}
	return util.Linked(body, util.SanitizeUrl(url), id, links, options, "")
}

func renderC4(db *C4Db, config types.Config, options types.RenderOptions) types.Rendered {
	return common.Tighten(options, func(tight bool) types.Rendered {
		if !tight {
			r, _ := draw(db, config, options, false, NARROW_GAP)
			return r
		}
		best, squeezed := draw(db, config, options, true, NARROW_GAP)
		for gap := NARROW_GAP + WIDEN; squeezed > 0 && gap <= NARROW_GAP+3*WIDEN; gap += WIDEN {
			wider, left := draw(db, config, options, true, float64(gap))
			if left < squeezed {
				best, squeezed = wider, left
			}
		}
		return best
	})
}

func draw(db *C4Db, _ types.Config, options types.RenderOptions, tight bool, gap float64) (types.Rendered, int) {
	size := types.Or(options.FontSize, 16)
	small := js.Round(size * 0.8125)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)
	links := []types.LinkInfo{}
	room := -1.0
	if tight {
		room = math.Max(80, types.Or(options.MaxWidth, 320)-float64(2*pad))
	}
	lay := layoutC4(db, measurer, size, small, room, gap)
	order, boxes := lay.order, lay.boxes
	root := order[0]

	minX := 0.0
	minY := 0.0
	maxX := root.w
	maxY := root.h
	grow := func(x0, y0, x1, y1 float64) {
		minX = math.Min(minX, x0)
		minY = math.Min(minY, y0)
		maxX = math.Max(maxX, x1)
		maxY = math.Max(maxY, y1)
	}

	ends := make([]*Box, 0, 2*len(db.rels))
	pairs := map[*Box]map[*Box]bool{}
	for _, rel := range db.rels {
		var a, b *Box
		if rel.fromAttr == nil {
			if from := db.getC4Shape(rel.from); from != nil {
				a = boxes[from]
			}
		}
		if rel.toAttr == nil {
			if to := db.getC4Shape(rel.to); to != nil {
				b = boxes[to]
			}
		}
		if a == nil || b == nil {
			types.Throw(`C4 rel "`+rel.from+`" -> "`+rel.to+`" references an unknown shape or boundary`, types.CodeSemantic, types.ErrorInfo{Type: "c4"})
		}
		ends = append(ends, a, b)
		targets := pairs[a]
		if targets == nil {
			targets = map[*Box]bool{}
			pairs[a] = targets
		}
		targets[b] = true
	}

	// What a relation's label should not cover: shapes, arrowheads, boundary headings, and the labels placed before it.
	nodes := []*Box{}
	var taken []float64
	for _, group := range order {
		for _, node := range group.nodes {
			nodes = append(nodes, &node.Box)
			taken = append(taken, node.x+EDGE, node.y+EDGE, node.x+node.w-EDGE, node.y+node.h-EDGE)
		}
	}
	rels := float64(len(db.rels))
	careful := rels*(float64(len(nodes)+len(order))+float64(3*rels)) <= CARE_BUDGET
	covered := func(x0, y0, x1, y1 float64) float64 {
		area := 0.0
		for i := 0; i < len(taken); i += 4 {
			w := math.Min(x1, taken[i+2]) - math.Max(x0, taken[i])
			if w <= 0 {
				continue
			}
			h := math.Min(y1, taken[i+3]) - math.Max(y0, taken[i+1])
			if h > 0 {
				area += float64(w * h)
			}
		}
		return area
	}
	offset := func(value float64) float64 {
		if !truthy(value) {
			return 0
		}
		return math.Max(-MAX_OFFSET, math.Min(MAX_OFFSET, value))
	}

	var geo *types.Geometry
	geoIndex := map[*Box]int{}
	if options.Geometry != nil {
		geo = &types.Geometry{}
	}

	// Every line is drawn before any label is placed, so that labels can keep off the arrowheads.
	var casings strings.Builder
	var edgesOut strings.Builder
	trim := svg.MarkerTrim("arrow_point")
	var fills []svg.Fill
	for _, group := range order {
		if group == root {
			continue
		}
		shape := userStyle(group.boundary.bgColor, "", "").Shape
		if svg.Paints(shape) {
			fills = append(fills, svg.Fill{Box: [4]float64{group.x, group.y, group.x + group.w, group.y + group.h}, Shape: shape})
		}
	}
	lanes := newLanes()
	for _, group := range order {
		if group != root {
			lanes.frame(&group.Box)
		}
	}
	var others []*Box
	if careful {
		others = nodes
	}
	routes := make([]Route, len(db.rels))
	for index, rel := range db.rels {
		a := ends[index*2]
		b := ends[index*2+1]
		style := userStyle("", rel.lineColor, "")
		startArrow := rel.typ == "birel" || rel.typ == "rel_b"
		endArrow := rel.typ != "rel_b"
		reverse := a != b && pairs[b][a]
		shift := 0.0
		if reverse {
			shift = PAIR_SHIFT
		}
		startTrim, endTrim := 0.0, 0.0
		if startArrow {
			startTrim = trim
		}
		if endArrow {
			endTrim = trim
		}
		r := route(a, b, shift, others, lanes, startTrim, endTrim)
		grow(math.Min(r.x0, r.x1), math.Min(r.y0, r.y1), math.Max(r.x0, r.x1), math.Max(r.y0, r.y1))
		if startArrow && careful {
			taken = append(taken, r.sx-ARROW, r.sy-ARROW, r.sx+ARROW, r.sy+ARROW)
		}
		if endArrow && careful {
			taken = append(taken, r.ex-ARROW, r.ey-ARROW, r.ex+ARROW, r.ey+ARROW)
		}
		over, onFills := svg.OverFills(r.d, []float64{r.sx, r.sy, r.ex, r.ey}, fills, style.Line, nil)
		color := over
		if color == "" {
			color = "var(--_l)"
			if style.Stroke != "" {
				color = svg.Esc(style.Stroke)
			}
		}
		if onFills && over == "" {
			startType, endType := "none", "none"
			if startArrow {
				startType = "arrow_point"
			}
			if endArrow {
				endType = "arrow_point"
			}
			path := svg.EdgePath{D: r.d, Sx: r.sx, Sy: r.sy, Sdx: r.sdx, Sdy: r.sdy, Ex: r.ex, Ey: r.ey, Edx: r.edx, Edy: r.edy}
			casings.WriteString(svg.EdgeCasing(path, startType, endType, style.Line, false))
		}
		stroke := ""
		if over != "" {
			stroke = ` stroke="` + over + `"`
		}
		svg.WriteStrings(&edgesOut, `<g class="pele-edge" data-id="`, svg.EscText(rel.from+"-"+rel.to), `"`, stroke, style.Line, `><path d="`, r.d, `"/>`)
		if startArrow {
			svg.WriteMarker(&edgesOut, "arrow_point", r.sx, r.sy, r.sdx, r.sdy, color)
		}
		if endArrow {
			svg.WriteMarker(&edgesOut, "arrow_point", r.ex, r.ey, r.edx, r.edy, color)
		}
		edgesOut.WriteString("</g>")
		routes[index] = r
		if geo != nil {
			e := types.GeoEdge{Stroke: "normal", From: -1, To: -1}
			if startArrow {
				e.Start = "arrow_point"
			}
			if endArrow {
				e.End = "arrow_point"
			}
			for k := 0; k+1 < len(r.points); k += 2 {
				axis := 1.0
				if k > 0 && math.Abs(r.points[k+1]-r.points[k-1]) > math.Abs(r.points[k]-r.points[k-2]) {
					axis = 0
				}
				e.Route = append(e.Route, r.points[k], r.points[k+1], axis)
			}
			geo.Edges = append(geo.Edges, e)
		}
	}

	// A boundary's heading goes at the left, the middle or the right of its top edge: the first of them no line crosses.
	for _, group := range order {
		if !careful || group.headW == 0 {
			continue
		}
		top := group.y + HEAD_TOP
		bottom := group.y + group.head - LANE
		fewest := math.Inf(1)
		for _, x := range [3]float64{HEAD_LEFT, (group.w - group.headW) / 2, group.w - HEAD_LEFT - group.headW} {
			count := 0
			for k := range routes {
				if crosses(routes[k].points, group.x+x-4, top, group.x+x+group.headW+4, bottom) {
					count++
				}
			}
			if float64(count) < fewest {
				fewest = float64(count)
				group.headX = x
			}
			if count == 0 {
				break
			}
		}
		taken = append(taken, group.x+group.headX-4, top, group.x+group.headX+group.headW+4, bottom)
	}

	var labelsOut strings.Builder
	for index, rel := range db.rels {
		r := &routes[index]
		id := rel.from + "-" + rel.to
		style := userStyle("", "", rel.textColor)
		t := rel.label
		if db.c4Type == "C4Dynamic" {
			t = strconv.Itoa(index+1) + ": " + t
		}
		label := text.LayoutLabel(t, false, measurer, small, r.room, 0)
		techn := ""
		if rel.techn != "" {
			techn = "[" + rel.techn + "]"
		}
		technLabel := text.LayoutLabel(techn, false, measurer, small, r.room, text.Italic)
		lw := math.Max(label.Width, technLabel.Width) + LABEL_PAD
		lh := label.Height + technLabel.Height
		if lh == 0 {
			continue
		}

		// The label sits on the line, or just beside it when another relation runs the opposite way.
		beside := float64(math.Abs(r.nx)*(lw/2)) + float64(math.Abs(r.ny)*(lh/2)) + 2
		aside := beside
		if truthy(r.lean) {
			aside = math.Max(0, math.Min(r.lean, beside-14))
		}
		// Manual offsets are tuned to the original spacing. Reposition compact labels beside
		// their routed arrows so an offset cannot leave them floating outside a boundary.
		extraX, extraY := 0.0, 0.0
		if !tight {
			extraX, extraY = offset(rel.offsetX), offset(rel.offsetY)
		}
		dx := float64(r.nx*aside) + extraX
		dy := float64(r.ny*aside) + extraY
		at := 0.5
		if careful {
			// Slide along the line to the spot that covers least. Offsets from the diagram are kept unless a spot
			// on the line itself covers less than any they lead to: they are often tuned for Mermaid's spacing.
			least := math.Inf(1)
			moved := dx != float64(r.nx*aside) || dy != float64(r.ny*aside)
			passes := 1
			if moved {
				passes = 2
			}
			for pass := 0; pass < passes && least > 0; pass++ {
				px, py := dx, dy
				if pass != 0 {
					px, py = float64(r.nx*aside), float64(r.ny*aside)
				}
				for _, t := range SLIDE {
					cx := r.x0 + float64((r.x1-r.x0)*t) + px
					cy := r.y0 + float64((r.y1-r.y0)*t) + py
					area := covered(cx-lw/2, cy-lh/2, cx+lw/2, cy+lh/2)
					if area < least {
						least = area
						at = t
						dx = px
						dy = py
					}
					if area == 0 {
						break
					}
				}
			}
		}
		cx := r.x0 + float64((r.x1-r.x0)*at) + dx
		top := r.y0 + float64((r.y1-r.y0)*at) + dy - lh/2
		if careful {
			taken = append(taken, cx-lw/2, top, cx+lw/2, top+lh)
		}
		grow(cx-lw/2, top, cx+lw/2, top+lh)
		svg.WriteStrings(&labelsOut, `<g class="pele-edge-label" data-id="`, svg.EscText(id), `">`)
		if rel.descr != "" {
			svg.WriteStrings(&labelsOut, `<title>`, svg.EscText(rel.descr), `</title>`)
		}
		svg.WriteStrings(&labelsOut, `<rect x="`, svg.Num(cx-lw/2), `" y="`, svg.Num(top), `" width="`, svg.Num(lw), `" height="`, svg.Num(lh), `" rx="3" fill="var(--_bg)"/>`,
			link(svg.LabelSvg(label, cx, top+label.Height/2, style.Text, nil)+
				svg.LabelSvg(technLabel, cx, top+label.Height+technLabel.Height/2, ` class="pele-c4-techn" fill="var(--_m)"`+style.Text, nil),
				rel.link, id, &links, options),
			`</g>`)
		if geo != nil {
			e := &geo.Edges[index]
			e.Label = append(label.Plain(), technLabel.Plain()...)
			e.LabelX, e.LabelY = cx, top+lh/2
		}
	}

	var clusters, nodesOut, nodeText strings.Builder
	for _, group := range order {
		if group != root {
			boundary := group.boundary
			style := userStyle(boundary.bgColor, boundary.borderColor, boundary.fontColor)
			color := svg.InkFill(style, "") + style.Text
			muted := svg.InkFill(style, "var(--_m)") + style.Text
			x := group.x + group.headX
			y := group.y + HEAD_TOP
			t := common.LeftLabelSvg(group.labels[0], x, y, ` class="pele-cluster-label" font-weight="var(--_hw)"`+color)
			y += group.labels[0].Height
			t += common.LeftLabelSvg(group.labels[1], x, y, ` class="pele-c4-type" font-size="`+js.Num(small)+`"`+muted)
			y += group.labels[1].Height
			t += common.LeftLabelSvg(group.labels[2], x, y, ` class="pele-c4-descr" font-size="`+js.Num(small)+`"`+muted)
			kind := "boundary"
			dash := ` stroke-dasharray="6 4"`
			if boundary.nodeType != "" {
				kind = "node"
				dash = ""
			}
			svg.WriteStrings(&clusters, `<g class="pele-cluster pele-c4-`, kind, `" data-id="`, svg.EscText(boundary.alias), `">`,
				`<rect x="`, svg.Num(group.x), `" y="`, svg.Num(group.y), `" width="`, svg.Num(group.w), `" height="`, svg.Num(group.h), `" fill="none" stroke="var(--_b)"`,
				dash, svg.Rounded(style.Shape), `/>`,

				link(t, boundary.link, boundary.alias, &links, options),
				`</g>`)
		}
		for _, node := range group.nodes {
			shape := node.shape
			external := strings.HasPrefix(shape.kind, "external")
			style := userStyle(shape.bgColor, shape.borderColor, shape.fontColor)
			stroke := ` stroke="var(--_b)"`
			fill := "--_s"
			if external {
				stroke = ` stroke="var(--_b)" stroke-dasharray="4 3"`
				fill = "--_bg"
			}
			w, h, form, labels := node.w, node.h, node.form, &node.labels
			dx := 0.0
			if form == "h-cyl" {
				dx = -queueRx(h) / 2
			}
			dy := 0.0
			if form == "person" {
				dy = HEAD / 2
			} else if form == "cyl" {
				dy = cylRy(w) / 2
			}
			color := svg.InkFill(style, "") + style.Text
			muted := svg.InkFill(style, "var(--_m)") + style.Text
			y := dy - node.textH/2
			nodeText.Reset()
			nodeText.WriteString(svg.DrawShape(form, w, h, ` fill="var(`+fill+`)"`+stroke+style.Shape, stroke+style.Line))
			nodeText.WriteString(svg.LabelSvg(labels[0], dx, y+labels[0].Height/2, ` class="pele-c4-type" font-size="`+js.Num(small-1)+`"`+muted, nil))
			y += labels[0].Height
			nodeText.WriteString(svg.LabelSvg(labels[1], dx, y+labels[1].Height/2, ` class="pele-label" font-weight="var(--_hw)"`+color, nil))
			y += labels[1].Height
			nodeText.WriteString(svg.LabelSvg(labels[2], dx, y+labels[2].Height/2, ` class="pele-c4-techn" font-size="`+js.Num(small)+`"`+muted, nil))
			y += labels[2].Height + 4
			nodeText.WriteString(svg.LabelSvg(labels[3], dx, y+labels[3].Height/2, ` class="pele-c4-descr" font-size="`+js.Num(small)+`"`+color, nil))
			ext := ""
			if external {
				ext = " pele-c4-external"
			}
			svg.WriteStrings(&nodesOut, `<g class="pele-node pele-shape-`, form, ` pele-c4-`, shape.kind, ext, `" data-id="`, svg.EscText(shape.alias),
				`" transform="translate(`, svg.Num(node.x+w/2), `,`, svg.Num(node.y+h/2), `)">`)
			if shape.link != "" {
				nodesOut.WriteString(link(nodeText.String(), shape.link, shape.alias, &links, options))
			} else {
				nodesOut.WriteString(nodeText.String())
			}
			nodesOut.WriteString(`</g>`)
		}
	}

	titleRoom := 4000.0
	if tight {
		titleRoom = common.TitleRoom(options)
	}
	title := text.LayoutLabel(db.title, false, measurer, size, titleRoom, text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 12
	}
	inner := math.Max(maxX-minX, title.Width)
	width := math.Ceil(inner + float64(2*pad))
	height := math.Ceil(maxY - minY + titleHeight + float64(2*pad))
	ox := pad - minX + (inner-(maxX-minX))/2
	oy := pad + titleHeight - minY

	var body strings.Builder
	svg.WriteStrings(&body, svg.LabelSvg(title, width/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil),
		`<g transform="translate(`, svg.Num(ox), `,`, svg.Num(oy), `)">`)
	if clusters.Len() > 0 {
		svg.WriteStrings(&body, `<g class="pele-clusters">`, clusters.String(), `</g>`)
	}
	body.WriteString(svg.CasingsSvg(casings.String()))
	if edgesOut.Len() > 0 {
		svg.WriteStrings(&body, `<g class="pele-edges" fill="none" stroke="var(--_l)" stroke-linecap="round">`, edgesOut.String(), `</g>`)
	}
	if nodesOut.Len() > 0 {
		svg.WriteStrings(&body, `<g class="pele-nodes">`, nodesOut.String(), `</g>`)
	}
	if labelsOut.Len() > 0 {
		svg.WriteStrings(&body, `<g class="pele-edge-labels" font-size="`, js.Num(small), `">`, labelsOut.String(), `</g>`)
	}
	body.WriteString(`</g>`)

	out := svg.SvgDocument("c4", width, height, size, options, svg.Accessible{AccTitle: db.accTitle, AccDescr: db.accDescr}, body.String())
	rendered := types.Rendered{SVG: out, Width: width, Height: height, Links: links}
	if geo != nil {
		fillGeometry(geo, geoIndex, order, ends, width, height, ox, oy)
		rendered.Geometry = geo
	}
	return rendered, lanes.squeezed
}

// fillGeometry gives the boundaries and shapes as sketch/text reads them: boundaries as groups,
// shapes as boxes with their lines of text, and the relations, all moved by the body's translate.
// Not in pele.
func fillGeometry(geo *types.Geometry, index map[*Box]int, order []*Group, ends []*Box, width, height, ox, oy float64) {
	geo.Width, geo.Height = width, height
	parentOf := func(group *Group) int {
		if p, ok := index[&group.Box]; ok {
			return p
		}
		return -1
	}
	groupOf := map[*C4Boundary]*Group{}
	for _, group := range order {
		groupOf[group.boundary] = group
	}
	for _, group := range order[1:] {
		index[&group.Box] = len(geo.Nodes)
		var label []string
		for _, l := range group.labels {
			label = append(label, l.Plain()...)
		}
		parent := -1
		if p := groupOf[group.boundary.parent]; p != nil {
			parent = parentOf(p)
		}
		geo.Nodes = append(geo.Nodes, types.GeoNode{ID: group.boundary.alias, X: group.x + group.w/2 + ox, Y: group.y + group.h/2 + oy,
			W: group.w, H: group.h, Label: label, Group: true, Parent: parent})
	}
	for _, group := range order {
		for _, node := range group.nodes {
			index[&node.Box] = len(geo.Nodes)
			var label []string
			for _, l := range node.labels {
				label = append(label, l.Plain()...)
			}
			shape := node.form
			if shape == "person" {
				shape = "rounded"
			}
			geo.Nodes = append(geo.Nodes, types.GeoNode{ID: node.shape.alias, Shape: shape, X: node.x + node.w/2 + ox, Y: node.y + node.h/2 + oy,
				W: node.w, H: node.h, Label: label, Parent: parentOf(group)})
		}
	}
	for k := range geo.Edges {
		e := &geo.Edges[k]
		e.From = index[ends[k*2]]
		e.To = index[ends[k*2+1]]
		for j := 0; j < len(e.Route); j += 3 {
			e.Route[j] += ox
			e.Route[j+1] += oy
		}
		e.LabelX += ox
		e.LabelY += oy
	}
}
