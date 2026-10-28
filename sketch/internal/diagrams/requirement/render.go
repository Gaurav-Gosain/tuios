// Ported from pele src/diagrams/requirement/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package requirement

import (
	"math"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/layout"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

const (
	NODE_SEP        = 40
	EDGE_SEP        = 16
	RANK_SEP        = 56
	PAD_X           = 14
	PAD_Y           = 10
	ROW_GAP         = 2
	COLUMN_GAP      = 12
	MIN_WIDTH       = 120
	NAME_WRAP       = 280
	VALUE_WRAP      = 240
	LOOP            = 26
	CONTAINS_RADIUS = 6
	ARROW_LENGTH    = 9
	ARROW_HALF      = 4.5
	END_STEP        = 28
	END_MARGIN      = 14
)

type Row struct {
	key    text.Label
	value  text.Label
	height float64
}

type NodeView struct {
	node       *GraphNode
	style      svg.ResolvedStyle
	kind       text.Label
	name       text.Label
	rows       []Row
	keyWidth   float64
	headHeight float64
	w          float64
	h          float64
}

// restyle adds bold or italic to a whole label after its markdown is read, since emphasis markers only
// toggle styles the label does not start with.
func restyle(label text.Label, bits int, measurer text.TextMeasurer) text.Label {
	if bits == 0 || len(label.Lines) == 0 {
		return label
	}
	width := 0.0
	widths := make([]float64, 0, len(label.Lines))
	lines := make([][]text.Span, len(label.Lines))
	for k, line := range label.Lines {
		total := 0.0
		spans := make([]text.Span, len(line))
		for j, span := range line {
			style := span.Style | bits
			w := span.Width
			if span.Icon == "" && span.Math == nil {
				w = measurer.Width(span.Text, label.Size, style)
			}
			total += w
			span.Style = style
			span.Width = w
			spans[j] = span
		}
		widths = append(widths, total)
		if total > width {
			width = total
		}
		lines[k] = spans
	}
	out := label
	out.Lines = lines
	out.Widths = widths
	out.Width = width
	return out
}

// startText writes a measured label with its lines starting at x, the first line's box starting at top.
func startText(b *strings.Builder, label text.Label, x, top float64, attrs string) {
	shared := svg.SharedStyle(label)
	var body, mathOut strings.Builder
	for i, line := range label.Lines {
		middle := top + text.LineMiddle(label, i)
		if text.HasMath(line) {
			mathOut.WriteString(svg.LabelSvg(text.LabelLine(label, i), x+label.Widths[i]/2, middle, attrs, nil))
			continue
		}
		y := middle + float64(label.Size*0.35)
		first := true
		for _, span := range line {
			if span.Icon != "" || span.Text == "" {
				continue
			}
			body.WriteString(`<tspan`)
			if first {
				body.WriteString(` x="`)
				svg.WriteNum(&body, x)
				body.WriteString(`" y="`)
				svg.WriteNum(&body, y)
				body.WriteByte('"')
			}
			svg.WriteStrings(&body, svg.SpanStyle(span.Style&^shared), `>`, svg.Esc(span.Text), `</tspan>`)
			first = false
		}
	}
	if body.Len() > 0 {
		svg.WriteStrings(b, `<text`, svg.TextStyle(attrs, shared), ` text-anchor="start" xml:space="preserve">`, body.String(), `</text>`)
	}
	b.WriteString(mathOut.String())
}

// containsMarker is the containment mark: a circled plus whose rim touches (x, y), where (dx, dy) points at the node.
func containsMarker(b *strings.Builder, x, y, dx, dy float64) {
	r := float64(CONTAINS_RADIUS)
	cx := x - float64(dx*r)
	cy := y - float64(dy*r)
	b.WriteString(`<circle class="pele-marker" cx="`)
	svg.WriteNum(b, cx)
	b.WriteString(`" cy="`)
	svg.WriteNum(b, cy)
	b.WriteString(`" r="6"/><path class="pele-marker" d="M`)
	svg.WriteNum(b, cx-float64(dx*r))
	b.WriteByte(',')
	svg.WriteNum(b, cy-float64(dy*r))
	b.WriteByte('L')
	svg.WriteNum(b, cx+float64(dx*r))
	b.WriteByte(',')
	svg.WriteNum(b, cy+float64(dy*r))
	b.WriteByte('M')
	svg.WriteNum(b, cx+float64(dy*r))
	b.WriteByte(',')
	svg.WriteNum(b, cy-float64(dx*r))
	b.WriteByte('L')
	svg.WriteNum(b, cx-float64(dy*r))
	b.WriteByte(',')
	svg.WriteNum(b, cy+float64(dx*r))
	b.WriteString(`"/>`)
}

// arrowMarker is an open arrowhead with its tip at (x, y), pointing along (dx, dy).
func arrowMarker(b *strings.Builder, x, y, dx, dy float64) {
	bx := x - float64(dx*ARROW_LENGTH)
	by := y - float64(dy*ARROW_LENGTH)
	b.WriteString(`<path class="pele-marker" d="M`)
	svg.WriteNum(b, bx-float64(dy*ARROW_HALF))
	b.WriteByte(',')
	svg.WriteNum(b, by+float64(dx*ARROW_HALF))
	b.WriteByte('L')
	svg.WriteNum(b, x)
	b.WriteByte(',')
	svg.WriteNum(b, y)
	b.WriteByte('L')
	svg.WriteNum(b, bx+float64(dy*ARROW_HALF))
	b.WriteByte(',')
	svg.WriteNum(b, by-float64(dx*ARROW_HALF))
	b.WriteString(`" stroke-linejoin="round"/>`)
}

type end struct {
	route  []float64
	at     int
	toward float64
}

// spreadEnds: the layout attaches every edge to the middle of a node's side. Ends that share a side are
// spread along it, in the order of where they head, so their markers do not sit on each other.
func spreadEnds(cnodes []*layout.CNode, cedges []*layout.CEdge, views []NodeView) {
	sides := map[int][]end{}
	var keys []int
	for _, edge := range cedges {
		route := edge.Route
		if edge.Src == edge.Dst || len(route) < 6 {
			continue
		}
		for _, at := range [2]int{0, len(route) - 3} {
			node := edge.Dst
			next := at - 3
			if at == 0 {
				node = edge.Src
				next = 3
			}
			c := cnodes[node]
			dx := route[at] - c.X
			dy := route[at+1] - c.Y
			var side int
			if math.Abs(math.Abs(dy)-c.H/2) < 0.5 {
				side = 2
				if dy < 0 {
					side = 0
				}
			} else if math.Abs(math.Abs(dx)-c.W/2) < 0.5 {
				side = 1
				if dx < 0 {
					side = 3
				}
			} else {
				continue
			}
			key := node*4 + side
			toward := route[next+1]
			if side%2 == 0 {
				toward = route[next]
			}
			if _, ok := sides[key]; !ok {
				keys = append(keys, key)
			}
			sides[key] = append(sides[key], end{route: route, at: at, toward: toward})
		}
	}
	for _, key := range keys {
		list := sides[key]
		if len(list) < 2 {
			continue
		}
		node := key >> 2
		vertical := key%2 == 1
		span := views[node].w - 2*END_MARGIN
		if vertical {
			span = views[node].h - 2*END_MARGIN
		}
		step := math.Max(0, math.Min(END_STEP, span/float64(len(list)-1)))
		sort.SliceStable(list, func(i, j int) bool { return list[i].toward < list[j].toward })
		for j := range list {
			offset := float64((float64(j) - float64(len(list)-1)/2) * step)
			if vertical {
				list[j].route[list[j].at+1] = cnodes[node].Y + offset
			} else {
				list[j].route[list[j].at] = cnodes[node].X + offset
			}
		}
	}
}

func renderRequirement(model *RequirementDb, config types.Config, options types.RenderOptions) types.Rendered {
	if !common.RunsAcross(model.direction) {
		return draw(model, config, options, false)
	}
	return common.TurnToFit(options, func(down bool) types.Rendered { return draw(model, config, options, down) })
}

// withoutDefault is classes.replace(/^default\s?/, ”).
func withoutDefault(classes string) string {
	if !strings.HasPrefix(classes, "default") {
		return classes
	}
	rest := classes[len("default"):]
	if rest != "" {
		r, size := utf8.DecodeRuneInString(rest)
		if js.IsSpace(r) {
			rest = rest[size:]
		}
	}
	return rest
}

// draw: `turned` draws a diagram that runs across as one that runs down.
func draw(model *RequirementDb, _ types.Config, options types.RenderOptions, turned bool) types.Rendered {
	graph := buildRequirementGraph(model)
	size := types.Or(options.FontSize, 16)
	small := js.Round(size * 0.875)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)

	index := map[string]int{}
	views := make([]NodeView, 0, len(graph.nodes))
	cnodes := make([]*layout.CNode, 0, len(graph.nodes))
	for k := range graph.nodes {
		node := &graph.nodes[k]
		style := svg.ResolveStyle(node.cssStyles)
		base := 0
		if style.Bold {
			base |= text.Bold
		}
		if style.Italic {
			base |= text.Italic
		}
		nameSize := size
		bodySize := small
		if style.FontSize != 0 {
			nameSize = style.FontSize
			bodySize = style.FontSize
		}
		requirement := node.requirement
		element := node.element
		kindText := "Element"
		if requirement != nil {
			kindText = requirement.typ
		}
		kind := restyle(text.LayoutLabel("«"+kindText+"»", false, measurer, bodySize, 4000, 0), base, measurer)
		name := restyle(text.LayoutLabel(node.id, true, measurer, nameSize, NAME_WRAP, 0), base|text.Bold, measurer)
		var fields [][2]string
		if requirement != nil {
			fields = [][2]string{
				{"ID", requirement.requirementId},
				{"Text", requirement.text},
				{"Risk", requirement.risk},
				{"Verification", requirement.verifyMethod},
			}
		} else {
			fields = [][2]string{
				{"Type", element.typ},
				{"Doc Ref", element.docRef},
			}
		}
		var rows []Row
		keyWidth := 0.0
		valueWidth := 0.0
		bodyHeight := 0.0
		for _, field := range fields {
			if field[1] == "" {
				continue
			}
			key := restyle(text.LayoutLabel(field[0], false, measurer, bodySize, 4000, 0), base, measurer)
			value := restyle(text.LayoutLabel(field[1], true, measurer, bodySize, VALUE_WRAP, 0), base, measurer)
			height := math.Max(key.Height, value.Height)
			rows = append(rows, Row{key: key, value: value, height: height})
			keyWidth = math.Max(keyWidth, key.Width)
			valueWidth = math.Max(valueWidth, value.Width)
			bodyHeight += height + ROW_GAP
		}
		if len(rows) > 0 {
			bodyHeight += 2*PAD_Y - ROW_GAP
		}
		headHeight := 2*PAD_Y + kind.Height + name.Height
		w := math.Max(MIN_WIDTH, math.Max(kind.Width, name.Width)+2*PAD_X)
		if len(rows) > 0 {
			w = math.Max(w, keyWidth+COLUMN_GAP+valueWidth+2*PAD_X)
		}
		// A requirement and an element may share a name; relationships then go to the requirement.
		if _, ok := index[node.id]; !ok {
			index[node.id] = len(views)
		}
		c := layout.Cnode(w, headHeight+bodyHeight, -1)
		c.Seq = len(views)
		views = append(views, NodeView{node: node, style: style, kind: kind, name: name, rows: rows, keyWidth: keyWidth, headHeight: headHeight, w: w, h: headHeight + bodyHeight})
		cnodes = append(cnodes, c)
	}

	rootDir := layout.Direction(graph.direction)
	if turned {
		rootDir = "TB"
	}
	sideways := rootDir == "LR" || rootDir == "RL"
	var cedges []*layout.CEdge
	var edgeLabels []text.Label
	var drawn []*GraphEdge
	for k := range graph.edges {
		edge := &graph.edges[k]
		if !edge.hasStart || !edge.hasEnd {
			continue
		}
		src, okS := index[edge.start]
		dst, okD := index[edge.end]
		if !okS || !okD {
			continue
		}
		label := text.LayoutLabel("«"+edge.typ+"»", false, measurer, small, 4000, 0)
		if src == dst {
			// A self-loop sits beside the node, across the flow, and the node's box grows to hold it.
			if sideways {
				cnodes[src].H += float64(2 * (LOOP + label.Height + 8))
			} else {
				cnodes[src].W += float64(2 * (LOOP + label.Width + 12))
			}
		}
		drawn = append(drawn, edge)
		edgeLabels = append(edgeLabels, label)
		cedges = append(cedges, &layout.CEdge{Src: src, Dst: dst, Minlen: 1, LabelW: label.Width + 12, LabelH: label.Height + 4})
	}

	lay := layout.CompoundLayout(cnodes, cedges, rootDir, layout.LayeredOptions{
		NodeSep: NODE_SEP,
		EdgeSep: EDGE_SEP,
		RankSep: RANK_SEP,
		PortSep: 20,
	})

	spreadEnds(cnodes, cedges, views)

	title := svg.TitleLabel(model.title, measurer, size, 4000)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 12
	}
	inner := math.Max(lay.Width, title.Width)
	ox := pad + (inner-lay.Width)/2
	oy := pad + titleHeight
	width := math.Ceil(inner + float64(2*pad))
	height := math.Ceil(lay.Height + titleHeight + float64(2*pad))

	var nodesOut strings.Builder
	for i := range views {
		view := &views[i]
		w, h, style := view.w, view.h, view.style
		color := svg.InkFill(style, "")
		muted := color
		if muted == "" {
			muted = ` fill="var(--_m)"`
		}
		left := -w / 2
		top := -h / 2
		kindClass := "pele-element"
		if view.node.requirement != nil {
			kindClass = "pele-requirement"
		}
		svg.WriteStrings(&nodesOut, `<g class="pele-node `, kindClass, svg.ClassNames(withoutDefault(view.node.cssClasses)), `" data-id="`, svg.EscText(view.node.id), `" transform="translate(`)
		svg.WriteNum(&nodesOut, cnodes[i].X+ox)
		nodesOut.WriteByte(',')
		svg.WriteNum(&nodesOut, cnodes[i].Y+oy)
		nodesOut.WriteString(`)"><rect x="`)
		svg.WriteNum(&nodesOut, left)
		nodesOut.WriteString(`" y="`)
		svg.WriteNum(&nodesOut, top)
		nodesOut.WriteString(`" width="`)
		svg.WriteNum(&nodesOut, w)
		nodesOut.WriteString(`" height="`)
		svg.WriteNum(&nodesOut, h)
		svg.WriteStrings(&nodesOut, `" fill="var(--_s)" stroke="var(--_b)"`, svg.Rounded(style.Shape), `/>`,

			svg.LabelSvg(view.kind, 0, top+PAD_Y+view.kind.Height/2, ` class="pele-stereotype" font-size="`+js.Num(view.kind.Size)+`"`+muted+style.Text, nil),
			svg.LabelSvg(view.name, 0, top+PAD_Y+view.kind.Height+view.name.Height/2, ` class="pele-label" font-size="`+js.Num(view.name.Size)+`" font-weight="var(--_hw)"`+color+style.Text, nil))
		if len(view.rows) > 0 {
			divider := top + view.headHeight
			nodesOut.WriteString(`<path class="pele-divider" d="M`)
			svg.WriteNum(&nodesOut, left)
			nodesOut.WriteByte(',')
			svg.WriteNum(&nodesOut, divider)
			nodesOut.WriteByte('H')
			svg.WriteNum(&nodesOut, -left)
			svg.WriteStrings(&nodesOut, `" stroke="var(--_b)"`, style.Line, `/><g class="pele-fields" font-size="`, js.Num(view.rows[0].key.Size), `">`)
			y := divider + PAD_Y
			for _, row := range view.rows {
				startText(&nodesOut, row.key, left+PAD_X, y, ` class="pele-field"`+muted+style.Text)
				startText(&nodesOut, row.value, left+PAD_X+view.keyWidth+COLUMN_GAP, y, ` class="pele-value"`+color+style.Text)
				y += row.height + ROW_GAP
			}
			nodesOut.WriteString(`</g>`)
		}
		nodesOut.WriteString(`</g>`)
	}

	var edgesOut, labelsOut strings.Builder
	var geo *types.Geometry
	if options.Geometry != nil {
		geo = geometry(views, cnodes, ox, oy, width, height)
	}
	loopSeen := map[int]int{}
	for i, ce := range cedges {
		edge := drawn[i]
		label := edgeLabels[i]
		startTrim := 0.0
		if edge.contains {
			startTrim = 2 * CONTAINS_RADIUS
		}
		var path svg.EdgePath
		labelX := ce.LabelX + ox
		labelY := ce.LabelY + oy
		var route []float64
		if ce.Src == ce.Dst {
			view := &views[ce.Src]
			cx := cnodes[ce.Src].X + ox
			cy := cnodes[ce.Src].Y + oy
			k := loopSeen[ce.Src]
			loopSeen[ce.Src] = k + 1
			half := view.h / 2
			if sideways {
				half = view.w / 2
			}
			spread := math.Min(half-4, 12+float64(float64(k)*6))
			reach := LOOP + float64(float64(k)*8)
			// Local frame: `out` points away from the node, `along` runs along its side.
			at := func(b *strings.Builder, out, along float64) {
				if sideways {
					svg.WriteNum(b, cx+along)
					b.WriteByte(',')
					svg.WriteNum(b, cy+view.h/2+out)
				} else {
					svg.WriteNum(b, cx+view.w/2+out)
					b.WriteByte(',')
					svg.WriteNum(b, cy+along)
				}
			}
			// A cubic whose control points are this far out peaks at `reach`.
			pull := reach / 0.75
			var d strings.Builder
			d.WriteByte('M')
			at(&d, startTrim, -spread)
			d.WriteByte('C')
			at(&d, pull, -spread)
			d.WriteByte(' ')
			at(&d, pull, spread)
			d.WriteByte(' ')
			at(&d, 0, spread)
			path.D = d.String()
			if sideways {
				path.Sx, path.Sy, path.Sdx, path.Sdy = cx-spread, cy+view.h/2, 0, -1
				path.Ex, path.Ey, path.Edx, path.Edy = cx+spread, cy+view.h/2, 0, -1
				labelX = cx
				labelY = cy + view.h/2 + reach + 4 + ce.LabelH/2
			} else {
				path.Sx, path.Sy, path.Sdx, path.Sdy = cx+view.w/2, cy-spread, -1, 0
				path.Ex, path.Ey, path.Edx, path.Edy = cx+view.w/2, cy+spread, -1, 0
				labelX = cx + view.w/2 + reach + 4 + ce.LabelW/2
				labelY = cy
			}
		} else {
			route = slices.Clone(ce.Route)
			for k := 0; k < len(route); k += 3 {
				route[k] += ox
				route[k+1] += oy
			}
			path = svg.RoutePath(route, "", startTrim, 0, svg.DefaultReach(startTrim), svg.DefaultReach(0))
		}
		relationship := "arrow"
		if edge.contains {
			relationship = "contains"
		}
		svg.WriteStrings(&edgesOut, `<g class="pele-edge pele-relationship-`, relationship, `" data-id="`, svg.EscText(edge.id), `"><path d="`, path.D, `"`)
		if !edge.contains {
			edgesOut.WriteString(` stroke-dasharray="5 4"`)
		}
		edgesOut.WriteString(`/>`)
		if edge.contains {
			containsMarker(&edgesOut, path.Sx, path.Sy, path.Sdx, path.Sdy)
		} else {
			arrowMarker(&edgesOut, path.Ex, path.Ey, path.Edx, path.Edy)
		}
		edgesOut.WriteString(`</g>`)
		labelsOut.WriteString(svg.EdgeLabelSvg(edge.id, label, labelX, labelY, "", nil))
		if geo != nil {
			e := types.GeoEdge{From: ce.Src, To: ce.Dst, Route: route, End: "arrow_point", Stroke: "dotted", Label: label.Plain(), LabelX: labelX, LabelY: labelY}
			if edge.contains {
				e.Start, e.End, e.Stroke = "arrow_circle", "", "normal"
			}
			geo.Edges = append(geo.Edges, e)
		}
	}

	parts := []string{svg.LabelSvg(title, width/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil)}
	if edgesOut.Len() > 0 {
		parts = append(parts, `<g class="pele-edges" fill="none" stroke="var(--_l)" stroke-linecap="round">`, edgesOut.String(), `</g>`)
	}
	if labelsOut.Len() > 0 {
		parts = append(parts, `<g class="pele-edge-labels" font-size="`, js.Num(small), `" fill="var(--_m)">`, labelsOut.String(), `</g>`)
	}
	parts = append(parts, `<g class="pele-nodes">`, nodesOut.String(), `</g>`)
	out := svg.SvgDocument("requirement", width, height, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr}, parts...)

	return types.Rendered{SVG: out, Width: width, Height: height, Links: []types.LinkInfo{}, Geometry: geo}
}

// geometry gives the requirements and elements as flowchart nodes, each with its stereotype, name
// and fields as lines, so that sketch/text draws the diagram as it draws a flowchart. The edges
// are added as they are drawn. Not in pele.
func geometry(views []NodeView, cnodes []*layout.CNode, ox, oy, width, height float64) *types.Geometry {
	g := &types.Geometry{Width: width, Height: height}
	for i := range views {
		view := &views[i]
		lines := append(view.kind.Plain(), view.name.Plain()...)
		for _, row := range view.rows {
			key := row.key.Plain()
			value := row.value.Plain()
			for k := range value {
				if k == 0 && len(key) > 0 {
					lines = append(lines, key[0]+": "+value[k])
				} else {
					lines = append(lines, "  "+value[k])
				}
			}
		}
		g.Nodes = append(g.Nodes, types.GeoNode{ID: view.node.id, Shape: "rect", X: cnodes[i].X + ox, Y: cnodes[i].Y + oy, W: view.w, H: view.h, Label: lines, Parent: -1})
	}
	return g
}
