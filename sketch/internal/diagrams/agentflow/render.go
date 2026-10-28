// Ported from pele src/diagrams/agentflow/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package agentflow

import (
	"math"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/diagrams/flowchart"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/layout"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
	"tuios.dev/sketch/internal/util"
)

// PALETTE is the number of series tokens, which is the palette Mermaid's slot rules are applied to.
const PALETTE = 8

const (
	NODE_SEP  = 40
	EDGE_SEP  = 16
	RANK_SEP  = 48
	GROUP_PAD = 20
	LOOP      = 26
	DOTS      = 14
	COLLAPSED = "collapsed"
)

// flowEnd is the side a flow runs toward, with sides numbered clockwise from the top.
func flowEnd(dir layout.Dir) int {
	switch dir {
	case "RL":
		return 3
	case "BT":
		return 0
	case "LR":
		return 1
	}
	return 2
}

// agentGraph builds the graph once per model. Building it also records the model's shape and containment diagnostics.
func agentGraph(db *AgentflowDb, config types.Config) *AgentGraph {
	if db.graph == nil {
		curve := ""
		if flow, ok := config["flowchart"].(map[string]any); ok {
			if s, ok := flow["curve"].(string); ok {
				curve = s
			}
		}
		db.graph = buildAgentGraph(db, PALETTE, curve)
	}
	return db.graph
}

func numberOption(config types.Config, key string, fallback float64) float64 {
	section, _ := config["agentflow"].(map[string]any)
	if value, ok := section[key].(float64); ok && value > 0 {
		return math.Min(value, 2000)
	}
	return fallback
}

// hasFill is setsFill: /[";]fill:/.test(shape).
func hasFill(shape string) bool { return svg.SetsFill(shape) }

// tints caches tint, by slot, wash and fill.
type tints map[int]string

func (t tints) of(slot int, wash string, style svg.ResolvedStyle) string {
	key := slot * 4
	if wash == "0.14" {
		key += 2
	}
	if hasFill(style.Shape) {
		key++
	}
	if s, ok := t[key]; ok {
		return s
	}
	s := tint(slot, wash, style)
	t[key] = s
	return s
}

// tint is a palette color as an outline with a light wash of the same color inside.
// A fill the author set replaces the wash and stays opaque.
func tint(slot int, wash string, style svg.ResolvedStyle) string {
	color := svg.SeriesColor(slot)
	if hasFill(style.Shape) {
		return ` fill="` + color + `" stroke="` + color + `"`
	}
	return ` fill="` + color + `" fill-opacity="` + wash + `" stroke="` + color + `"`
}

type NodeView struct {
	node  *GraphNode
	style svg.ResolvedStyle
	label text.Label
	shape string
	w     float64
	h     float64
	dy    float64
	loops int
}

func renderAgentflow(db *AgentflowDb, config types.Config, options types.RenderOptions) types.Rendered {
	dir := ""
	if db.Direction != nil {
		dir = *db.Direction
	}
	if !common.RunsAcross(dir) {
		return draw(db, config, options, false)
	}
	return common.TurnToFit(options, func(down bool) types.Rendered { return draw(db, config, options, down) })
}

// trimDefault is classes.replace(/^default\s?/, ”).
func trimDefault(classes string) string {
	if !strings.HasPrefix(classes, "default") {
		return classes
	}
	rest := classes[len("default"):]
	if rest == "" {
		return rest
	}
	r, size := utf8.DecodeRuneInString(rest)
	if js.IsSpace(r) {
		return rest[size:]
	}
	return rest
}

var edgeClassWords = [...]string{"edge-thickness-normal", "edge-pattern-solid", "flowchart-link"}

// trimEdgeClasses is classes.replace(/edge-thickness-normal|edge-pattern-solid|flowchart-link/g, ”).
func trimEdgeClasses(classes string) string {
	var b strings.Builder
	for i := 0; i < len(classes); {
		matched := false
		for _, w := range edgeClassWords {
			if strings.HasPrefix(classes[i:], w) {
				i += len(w)
				matched = true
				break
			}
		}
		if !matched {
			b.WriteByte(classes[i])
			i++
		}
	}
	return b.String()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type heldEdge struct {
	edge     *GraphEdge
	label    text.Label
	src, dst int
}

// draw: `turned` draws a diagram that runs across as one that runs down.
func draw(db *AgentflowDb, config types.Config, options types.RenderOptions, turned bool) types.Rendered {
	graph := agentGraph(db, config)
	size := types.Or(options.FontSize, 16)
	edgeSize := js.Round(size * 0.875)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	wrapWidth := numberOption(config, "wrappingWidth", 120)
	minWidth := numberOption(config, "minNodeWidth", 120)
	pad := types.Or(options.Padding, 8)
	icons := options.Icons

	index := make(map[string]int, len(graph.Nodes))
	views := make([]NodeView, 0, len(graph.Nodes))
	cnodes := make([]*layout.CNode, 0, len(graph.Nodes))
	for i, node := range graph.Nodes {
		index[node.ID] = i
	}

	for _, node := range graph.Nodes {
		styles := node.CssCompiledStyles
		if len(node.CssStyles) > 0 {
			styles = append(append([]string{}, node.CssCompiledStyles...), node.CssStyles...)
		}
		style := svg.ResolveStyle(styles)
		textStyle := 0
		if style.Bold {
			textStyle |= text.Bold
		}
		if style.Italic {
			textStyle |= text.Italic
		}
		markdown := node.LabelType == "markdown"
		fontSize := size
		if style.FontSize != 0 {
			fontSize = style.FontSize
		}
		var view NodeView
		var c *layout.CNode
		switch {
		case node.IsGroup:
			label := text.LayoutLabel(deref(node.Label), markdown, measurer, fontSize, 4000, textStyle)
			view = NodeView{node: node, style: style, label: label, shape: "group"}
			c = layout.Cnode(0, 0, -1)
			c.IsGroup = true
			if node.Dir != nil && *node.Dir != "" {
				c.Dir = layout.Direction(*node.Dir)
			}
			c.PadX = GROUP_PAD
			c.PadTop = GROUP_PAD
			if label.Height > 0 {
				c.PadTop = label.Height + 16
			}
			c.PadBottom = GROUP_PAD
			c.MinW = label.Width + 2*GROUP_PAD - 12
		case node.Shape == "collapsedGroup":
			label := text.LayoutLabel(deref(node.Label), markdown, measurer, fontSize, wrapWidth, textStyle)
			w := math.Max(label.Width, minWidth) + 2*svg.PAD_X
			h := label.Height + 2*svg.PAD_Y + DOTS
			view = NodeView{node: node, style: style, label: label, shape: COLLAPSED, w: w, h: h, dy: -DOTS / 2}
			c = layout.Cnode(w, h, -1)
		default:
			shape := flowchart.CanonicalShape(node.Shape)
			label := text.LayoutLabel(deref(node.Label), markdown, measurer, fontSize, wrapWidth, textStyle)
			// A diamond grows twice as fast as its label, so it keeps the label's own width.
			tw := math.Max(label.Width, minWidth)
			if shape == "diam" {
				tw = label.Width
			}
			s := svg.ShapeSizeOf(shape, tw, label.Height)
			view = NodeView{node: node, style: style, label: label, shape: shape, w: s.W, h: s.H, dy: s.Dy}
			c = layout.Cnode(s.W, s.H, -1)
		}
		c.Seq = len(views)
		views = append(views, view)
		cnodes = append(cnodes, c)
	}
	for i, node := range graph.Nodes {
		cnodes[i].Parent = -1
		if node.HasParent {
			if p, ok := index[node.ParentID]; ok {
				cnodes[i].Parent = p
			}
		}
	}
	layout.InheritCrossedDirections(cnodes, len(graph.Edges), func(i int) (int, int) {
		src, dst := -1, -1
		if k, ok := index[graph.Edges[i].Start]; ok {
			src = k
		}
		if k, ok := index[graph.Edges[i].End]; ok {
			dst = k
		}
		return src, dst
	})

	rootDir := layout.Direction(deref(db.Direction))
	if turned {
		rootDir = "TB"
	}
	// flowOf is the direction of the level a node sits in.
	flowOf := func(i int) layout.Dir {
		var dir layout.Dir
		for p, hops := cnodes[i].Parent, 0; p >= 0 && dir == "" && hops < 10000; p, hops = cnodes[p].Parent, hops+1 {
			dir = cnodes[p].Dir
		}
		if dir == "" {
			return rootDir
		}
		return dir
	}
	flowsSideways := func(i int) bool {
		dir := flowOf(i)
		return dir == "LR" || dir == "RL"
	}

	depth := make([]int, len(cnodes))
	for k := range depth {
		depth[k] = -1
	}
	var chain []int
	for i := range cnodes {
		chain = chain[:0]
		j := i
		for j >= 0 && depth[j] < 0 && len(chain) <= len(cnodes) {
			chain = append(chain, j)
			j = cnodes[j].Parent
		}
		d := 0
		if j >= 0 {
			d = depth[j] + 1
		}
		for k := len(chain) - 1; k >= 0; k-- {
			depth[chain[k]] = d
			d++
		}
	}
	contains := func(group, i int) bool {
		j := i
		for d := depth[i]; d > depth[group]; d-- {
			j = cnodes[j].Parent
		}
		return j == group && i != group
	}

	cedges := make([]*layout.CEdge, 0, len(graph.Edges))
	edgeLabels := make([]text.Label, 0, len(graph.Edges))
	drawn := make([]*GraphEdge, 0, len(graph.Edges))
	// Edges between a node and a container around it. The layout has no place for them, so they
	// are drawn afterwards, between the node and the container's border.
	var held []heldEdge
	for _, edge := range graph.Edges {
		src, ok1 := index[edge.Start]
		dst, ok2 := index[edge.End]
		if !ok1 || !ok2 {
			continue
		}
		label := text.LayoutLabel(edge.Label, edge.LabelType == "markdown", measurer, edgeSize, wrapWidth, 0)
		if contains(src, dst) || contains(dst, src) {
			held = append(held, heldEdge{edge, label, src, dst})
			continue
		}
		if src == dst {
			// A self-loop sits beside the node, across the flow, and the node's box grows to hold it.
			views[src].loops++
			if flowsSideways(src) {
				room := 0.0
				if label.Height > 0 {
					room = label.Height + 8
				}
				cnodes[src].H += float64(2 * (LOOP + room))
			} else {
				room := 0.0
				if label.Width > 0 {
					room = label.Width + 12
				}
				cnodes[src].W += float64(2 * (LOOP + room))
			}
		}
		drawn = append(drawn, edge)
		edgeLabels = append(edgeLabels, label)
		minlen := 1
		if edge.Minlen >= 0 {
			minlen = edge.Minlen
		}
		ce := &layout.CEdge{Src: src, Dst: dst, Minlen: minlen, Route: []float64{}}
		if label.Width > 0 {
			ce.LabelW = label.Width + 12
		}
		if label.Height > 0 {
			ce.LabelH = label.Height + 4
		}
		cedges = append(cedges, ce)
	}

	lay := layout.CompoundLayout(cnodes, cedges, rootDir, layout.LayeredOptions{
		NodeSep: numberOption(config, "nodeSpacing", NODE_SEP),
		EdgeSep: EDGE_SEP,
		RankSep: numberOption(config, "rankSpacing", RANK_SEP),
		PortSep: 20,
	})

	title := text.LayoutLabel(db.Title, false, measurer, size, 4000, text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 12
	}
	inner := math.Max(lay.Width, title.Width)
	ox := pad + (inner-lay.Width)/2
	oy := pad + titleHeight
	layout.ShiftLayout(cnodes, cedges, ox, oy)
	width := math.Ceil(inner + float64(2*pad))
	height := math.Ceil(lay.Height + titleHeight + float64(2*pad))
	links := []types.LinkInfo{}

	shapeView := func(i int) svg.ShapeView { return svg.ShapeView{Shape: views[i].shape, W: views[i].w, H: views[i].h} }

	// An edge between a node and a container around it runs straight between the node and the
	// container's border: out along the flow, or in against it. If another node is in the way it
	// takes a side instead, and failing that the far end.
	heldPaths := make([]svg.EdgePath, 0, len(held))
	heldLabels := make([]float64, 0, 2*len(held))
	heldRoutes := make([][]float64, 0, len(held))
	careful := len(held)*len(cnodes) < 2e6
	for _, h := range held {
		out := contains(h.dst, h.src)
		outer, i := h.src, h.dst
		if out {
			outer, i = h.dst, h.src
		}
		g := cnodes[outer]
		n := cnodes[i]
		gdir := g.Dir
		if gdir == "" {
			gdir = flowOf(outer)
		}
		end := flowEnd(gdir)
		first := (end + 2) & 3
		if out {
			first = end
		}
		toward := func(side int) []float64 {
			upright := (side & 1) == 0
			sign := 1.0
			if side == 0 || side == 3 {
				sign = -1
			}
			nx, ny, bx, by, axis := n.X+float64(sign*n.W)/2, n.Y, g.X+float64(sign*g.W)/2, n.Y, 1.0
			if upright {
				nx, ny, bx, by, axis = n.X, n.Y+float64(sign*n.H)/2, n.X, g.Y+float64(sign*g.H)/2, 0
			}
			if out {
				return []float64{nx, ny, axis, bx, by, axis}
			}
			return []float64{bx, by, axis, nx, ny, axis}
		}
		blocked := func(r []float64) bool {
			for k, c := range cnodes {
				if k != i && !c.IsGroup &&
					c.X+c.W/2 > math.Min(r[0], r[3])-1 &&
					c.X-c.W/2 < math.Max(r[0], r[3])+1 &&
					c.Y+c.H/2 > math.Min(r[1], r[4])-1 &&
					c.Y-c.H/2 < math.Max(r[1], r[4])+1 {
					return true
				}
			}
			return false
		}
		route := toward(first)
		if careful && blocked(route) {
			for _, side := range [3]int{(first + 1) & 3, (first + 3) & 3, (first + 2) & 3} {
				other := toward(side)
				if !blocked(other) {
					route = other
					break
				}
			}
		}
		heldRoutes = append(heldRoutes, route)
		heldLabels = append(heldLabels, (route[0]+route[3])/2, (route[1]+route[4])/2)
		drawnRoute := append([]float64{}, route...)
		at := 3
		if out {
			at = 0
		}
		svg.InsetRoute(drawnRoute, at, shapeView(i), n)
		trim := svg.MarkerTrim(h.edge.ArrowTypeEnd)
		heldPaths = append(heldPaths, svg.RoutePath(drawnRoute, "linear", 0, trim, 0, svg.DefaultReach(trim)))
	}

	crossings := svg.Crossings{}
	anyGroup := false
	for _, c := range cnodes {
		if c.IsGroup {
			anyGroup = true
		}
	}
	if anyGroup {
		for _, e := range cedges {
			svg.MarkCrossings(crossings, e.Route)
		}
		for _, route := range heldRoutes {
			svg.MarkCrossings(crossings, route)
		}
	}

	tinted := tints{}
	var clusters, nodesOut strings.Builder
	var fills []svg.Fill
	nodesOut.Grow(len(views) * 320)
	for i := range views {
		view := &views[i]
		c := cnodes[i]
		node := view.node
		x := c.X
		y := c.Y
		classes := svg.ClassNames(trimDefault(node.CssClasses))
		id := svg.EscText(node.ID)
		slot := KIND_COUNT
		if node.ColorIndex != nil {
			slot = *node.ColorIndex
		}
		if c.IsGroup {
			if svg.Paints(view.style.Shape) {
				fills = append(fills, svg.Fill{Box: [4]float64{x - c.W/2, y - c.H/2, x + c.W/2, y + c.H/2}, Shape: view.style.Shape})
			}
			titleX := svg.ClusterTitleX(c, view.label.Width, crossings, GROUP_PAD)
			svg.WriteStrings(&clusters, `<g class="pele-cluster pele-flow`, classes, `" data-id="`, id, `"><rect x="`, svg.Num(x-c.W/2), `" y="`, svg.Num(y-c.H/2),
				`" width="`, svg.Num(c.W), `" height="`, svg.Num(c.H), `"`, svg.Rounded(tinted.of(slot, "0.06", view.style)+view.style.Shape), `/>`,
				svg.LabelSvg(view.label, titleX, y-c.H/2+8+view.label.Height/2, ` class="pele-cluster-label"`+svg.InkFill(view.style, "var(--_m)")+view.style.Text, icons), `</g>`)
			continue
		}

		w := view.w
		h := view.h
		kind := ""
		var shape string
		if view.shape == COLLAPSED {
			color := svg.SeriesColor(slot)
			line := h/2 - DOTS - 2
			dots := line + (DOTS+2)/2
			shape = `<rect x="` + svg.Num(-w/2) + `" y="` + svg.Num(-h/2) + `" width="` + svg.Num(w) + `" height="` + svg.Num(h) + `"` +
				svg.Rounded(tinted.of(slot, "0.06", view.style)+view.style.Shape) + `/>` +

				`<path d="M` + svg.Num(-w/2+10) + `,` + svg.Num(line) + `H` + svg.Num(w/2-10) + `" fill="none" stroke="` + color + `" stroke-dasharray="3 3"` + view.style.Line + `/>` +
				`<path class="pele-collapsed-mark" d="` + dot(-9, dots) + dot(0, dots) + dot(9, dots) + `" fill="` + color + `" stroke="none"/>`
		} else {
			nodeKind := node.Kind
			if nodeKind == "" {
				nodeKind = "task"
			}
			kslot, ok := KIND_SLOT[nodeKind]
			if !ok {
				kslot = 1
			}
			kind = " pele-kind-" + nodeKind
			shape = svg.DrawShape(view.shape, w, h, tinted.of(kslot, "0.14", view.style)+view.style.Shape, ` stroke="`+svg.SeriesColor(kslot)+`"`+view.style.Line)
		}

		ink := ""
		if view.shape == COLLAPSED || svg.ShapeFilled(view.shape) {
			ink = svg.InkFill(view.style, "")
		}
		label := svg.LabelSvg(view.label, 0, view.dy, ` class="pele-label"`+ink+view.style.Text, icons)
		// / internal-link(?: |$)/ on classes + " ".
		internal := strings.Contains(classes+" ", " internal-link ")
		if internal && deref(node.Label) != "" && !(options.Links != nil && !*options.Links) {
			// The label is a note name, but it still ends up in an href, so it gets the same check as a URL.
			name := text.DecodeEntities(*node.Label)
			safe := util.InternalLink(name, options)
			target := svg.Esc(name)
			href := ""
			if safe {
				href = ` href="` + target + `"` + util.RelAttr(options)
			}
			label = `<a class="internal-link"` + href + ` data-href="` + target + `">` + label + `</a>`
			links = append(links, types.LinkInfo{ID: node.ID, Href: name, Internal: true})
		}
		body := shape + label
		if node.Tooltip != "" {
			body = `<title>` + svg.EscText(node.Tooltip) + `</title>` + body
		}
		if node.Link != nil && *node.Link != "" {
			attrs := ""
			if node.LinkTarget != nil && *node.LinkTarget != "" {
				attrs = ` target="` + svg.Esc(*node.LinkTarget) + `"`
			}
			body = util.Linked(body, *node.Link, node.ID, &links, options, attrs)
		}
		svg.WriteStrings(&nodesOut, `<g class="pele-node pele-shape-`, view.shape, kind, classes, `" data-id="`, id, `" transform="translate(`, svg.Num(x), `,`, svg.Num(y), `)">`, body, `</g>`)
	}

	var geo *types.Geometry
	if options.Geometry != nil {
		geo = geometry(views, cnodes, width, height)
	}

	var casings, edgesOut, labelsOut strings.Builder
	edgesOut.Grow((len(cedges) + len(held)) * 200)
	put := func(edge *GraphEdge, label text.Label, path svg.EdgePath, x, y float64, src, dst int, route []float64) {
		var style *svg.ResolvedStyle
		if len(edge.Style) > 0 || len(edge.CssCompiledStyles) > 0 {
			s := svg.ResolveStyle(append(append([]string{}, edge.CssCompiledStyles...), edge.Style...))
			style = &s
		}
		endType := edge.ArrowTypeEnd
		line := ""
		if style != nil {
			line = style.Line
		}
		over, onFill := svg.OverFills(path.D, []float64{path.Ex, path.Ey}, fills, line, path.Tokens)
		color := over
		if color == "" {
			color = "var(--_l)"
			if style != nil && style.Stroke != "" {
				color = svg.Esc(style.Stroke)
			}
		}
		dotted := edge.Thickness == "dotted"
		kind := "sequence"
		if dotted {
			kind = "reference"
		} else if endType == "arrow_cross" {
			kind = "failure"
		}
		if onFill && over == "" {
			casings.WriteString(svg.EdgeCasing(path, "none", endType, line, false))
		}
		overAttr := ""
		if over != "" {
			overAttr = ` stroke="` + over + `"`
		}
		svg.WriteStrings(&edgesOut, `<g class="pele-edge pele-edge-`, kind, svg.ClassNames(trimEdgeClasses(edge.Classes)), `" data-id="`, svg.EscText(edge.ID), `"`, overAttr, line, `><path d="`, path.D, `"`)
		if dotted {
			edgesOut.WriteString(` stroke-dasharray="3 4"`)
		}
		edgesOut.WriteString(`/>`)
		svg.WriteMarker(&edgesOut, endType, path.Ex, path.Ey, path.Edx, path.Edy, color)
		edgesOut.WriteString(`</g>`)
		if label.Width > 0 {
			textAttrs := ""
			if style != nil {
				textAttrs = style.Text
			}
			labelsOut.WriteString(svg.EdgeLabelSvg(edge.ID, label, x, y, textAttrs, icons))
		}
		if geo != nil {
			stroke := "normal"
			if dotted {
				stroke = "dotted"
			}
			geo.Edges = append(geo.Edges, types.GeoEdge{From: src, To: dst, Route: route, Start: "none", End: endType, Stroke: stroke, Label: label.Plain(), LabelX: x, LabelY: y})
		}
	}

	loopSeen := map[int]int{}
	for i, ce := range cedges {
		edge := drawn[i]
		label := edgeLabels[i]
		endType := edge.ArrowTypeEnd
		var path svg.EdgePath
		var route []float64
		if ce.Src == ce.Dst {
			view := &views[ce.Src]
			k := loopSeen[ce.Src]
			loopSeen[ce.Src] = k + 1
			ll := svg.LoopLabel{LabelW: ce.LabelW, LabelH: ce.LabelH, LabelX: ce.LabelX, LabelY: ce.LabelY}
			c := cnodes[ce.Src]
			path = svg.LoopPath(c.X, c.Y, view.w, view.h, k, flowsSideways(ce.Src), LOOP, svg.MarkerTrim(endType), &ll)
			ce.LabelX, ce.LabelY = ll.LabelX, ll.LabelY
		} else {
			route = append([]float64{}, ce.Route...)
			svg.InsetRoute(route, 0, shapeView(ce.Src), cnodes[ce.Src])
			svg.InsetRoute(route, len(route)-3, shapeView(ce.Dst), cnodes[ce.Dst])
			trim := svg.MarkerTrim(endType)
			path = svg.RoutePath(route, edge.Curve, 0, trim, 0, svg.DefaultReach(trim))
		}
		put(edge, label, path, ce.LabelX, ce.LabelY, ce.Src, ce.Dst, route)
	}
	for i, h := range held {
		put(h.edge, h.label, heldPaths[i], heldLabels[2*i], heldLabels[2*i+1], h.src, h.dst, heldRoutes[i])
	}

	parts := []string{svg.LabelSvg(title, width/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil)}
	if clusters.Len() > 0 {
		parts = append(parts, `<g class="pele-clusters">`, clusters.String(), `</g>`)
	}
	if casings.Len() > 0 {
		parts = append(parts, svg.CasingsSvg(casings.String()))
	}
	if edgesOut.Len() > 0 {
		parts = append(parts, `<g class="pele-edges" fill="none" stroke="var(--_l)" stroke-linecap="round">`, edgesOut.String(), `</g>`)
	}
	if labelsOut.Len() > 0 {
		parts = append(parts, `<g class="pele-edge-labels" font-size="`, js.Num(edgeSize), `">`, labelsOut.String(), `</g>`)
	}
	parts = append(parts, `<g class="pele-nodes">`, nodesOut.String(), `</g>`)
	out := svg.SvgDocument("agentflow", width, height, size, options, svg.Accessible{AccTitle: db.AccTitle, AccDescr: db.AccDescr}, parts...)

	return types.Rendered{SVG: out, Width: width, Height: height, Links: links, Geometry: geo}
}

func dot(x, y float64) string {
	return `M` + svg.Num(x-1.75) + `,` + svg.Num(y) + `a1.75,1.75 0 1 0 3.5,0a1.75,1.75 0 1 0 -3.5,0Z`
}

// textShapes are the shapes sketch/text knows for the shapes agentflow draws.
var textShapes = map[string]string{"rounded": "rounded", "fr-rect": "rect", "lean-r": "rect", "diam": "diam", "lin-doc": "rect", "hex": "hex", "collapsed": "rect"}

// geometry gives the nodes as flowchart nodes and the flows as groups, so that sketch/text
// draws the diagram as it draws a flowchart. The edges are added as they are drawn. Not in pele.
func geometry(views []NodeView, cnodes []*layout.CNode, width, height float64) *types.Geometry {
	g := &types.Geometry{Width: width, Height: height}
	for i := range views {
		view := &views[i]
		c := cnodes[i]
		shape, ok := textShapes[view.shape]
		if !ok {
			shape = view.shape
		}
		n := types.GeoNode{ID: view.node.ID, Shape: shape, X: c.X, Y: c.Y, W: view.w, H: view.h, Label: view.label.Plain(), Group: c.IsGroup, Parent: c.Parent}
		if c.IsGroup {
			n.W, n.H = c.W, c.H
		}
		if view.shape == COLLAPSED {
			n.Label = append(n.Label, "···")
		}
		g.Nodes = append(g.Nodes, n)
	}
	return g
}
