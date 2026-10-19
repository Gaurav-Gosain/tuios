// Ported from pele src/diagrams/flowchart/render.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package flowchart

import (
	"math"
	"regexp"
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/layout"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
	"tuios.dev/sketch/internal/util"
)

const (
	NODE_SEP    = 40
	EDGE_SEP    = 16
	RANK_SEP    = 48
	GROUP_PAD   = 20
	LOOP        = 26
	SHAPE_ATTRS = ` fill="var(--_s)" stroke="var(--_b)"`
	LINE_ATTRS  = ` stroke="var(--_b)"`
)

func numberOption(section types.Config, key string, fallback float64) float64 {
	if value, ok := section[key].(float64); ok && value > 0 {
		return math.Min(value, 2000)
	}
	return fallback
}

// FlowVariant is a diagram type that is a flowchart with a different layout, which may draw its top-level groups itself.
type FlowVariant interface {
	Type() string
	Prepare(graph *FlowGraph)
	Layout(nodes []*layout.CNode, edges []*layout.CEdge, dir layout.Dir, opt layout.LayeredOptions, tight bool) layout.CompoundResult
	// DrawGroup returns false where the variant does not draw groups itself.
	DrawGroup(c *layout.CNode, label text.Label, style svg.ResolvedStyle, classes string, id string, dir layout.Dir, icons text.IconResolver) (string, bool)
}

func assetSize(value *float64, fallback float64) float64 {
	if value != nil && *value > 0 && *value <= 4000 {
		return *value
	}
	return fallback
}

type nodeView struct {
	node  *GraphNode
	style svg.ResolvedStyle
	label text.Label
	shape string
	w     float64
	h     float64
	dy    float64
	loops int
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func renderFlowchart(db *FlowDb, config types.Config, options types.RenderOptions, variant FlowVariant) types.Rendered {
	fitted := func(down bool) types.Rendered {
		return common.Tighten(options, func(tight bool) types.Rendered { return draw(db, config, options, variant, down, tight) })
	}
	if common.RunsAcross(deref(db.Direction)) {
		return common.TurnToFit(options, fitted)
	}
	return fitted(false)
}

var (
	reDefaultClass = regexp.MustCompile(`^default[` + js.S + `]?`)
	reInternalLink = regexp.MustCompile(` internal-link(?: |$)`)
	reEdgeClasses  = regexp.MustCompile(`edge-thickness-normal|edge-pattern-solid|flowchart-link`)
	reStep         = regexp.MustCompile(`^step`)
)

// draw: `turned` draws a flowchart that runs across as one that runs down. `tight` sets nodes closer together.
func draw(db *FlowDb, config types.Config, options types.RenderOptions, variant FlowVariant, turned bool, tight bool) types.Rendered {
	groupPad := float64(GROUP_PAD)
	if tight {
		groupPad = 12
	}
	var flow types.Config
	if variant != nil {
		if own, ok := config[variant.Type()].(map[string]any); ok {
			flow = own
		}
	}
	if flow == nil {
		flow, _ = config["flowchart"].(map[string]any)
		if flow == nil {
			flow = types.Config{}
		}
	}
	curve, _ := flow["curve"].(string)
	graph := buildFlowGraph(db, curve)
	if variant != nil {
		variant.Prepare(&graph)
	}
	size := types.Or(options.FontSize, 16)
	edgeSize := js.Round(size * 0.875)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, "")
	}
	wrapLimit := math.Inf(1)
	if variant != nil && tight {
		wrapLimit = 100
	}
	wrapWidth := math.Min(numberOption(flow, "wrappingWidth", 200), wrapLimit)
	pad := types.Or(options.Padding, 8)
	icons := options.Icons

	index := map[string]int{}
	var views []*nodeView
	var cnodes []*layout.CNode
	for i, node := range graph.Nodes {
		index[node.ID] = i
	}

	for _, node := range graph.Nodes {
		decls := node.CssCompiledStyles
		if len(node.CssStyles) > 0 {
			decls = append(append([]string{}, node.CssCompiledStyles...), node.CssStyles...)
		}
		style := svg.ResolveStyle(decls)
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
		var view *nodeView
		var c *layout.CNode
		if node.IsGroup {
			max := 4000.0
			if variant != nil && tight {
				max = math.Max(80, common.TitleRoom(options)/2)
			}
			label := text.LayoutLabel(deref(node.Label), markdown, measurer, fontSize, max, textStyle)
			view = &nodeView{node: node, style: style, label: label, shape: "group"}
			c = layout.Cnode(0, 0, -1)
			c.IsGroup = true
			if node.Dir != "" {
				c.Dir = layout.Direction(node.Dir)
			}
			c.PadX = groupPad
			if label.Height > 0 {
				c.PadTop = label.Height + 16
			} else {
				c.PadTop = GROUP_PAD
			}
			c.PadBottom = groupPad
			c.MinW = label.Width + 2*groupPad - 12
		} else {
			shape := canonicalShape(node.Shape)
			labelText := ""
			if node.Img != "" || node.Icon != "" || svg.ShapeHasLabel(shape) {
				labelText = deref(node.Label)
			}
			label := text.LayoutLabel(labelText, markdown, measurer, fontSize, wrapWidth, textStyle)
			var w, h, dy float64
			if node.Img != "" || node.Icon != "" {
				box := 48.0
				if node.Img != "" {
					box = 80
				}
				aw := assetSize(node.AssetWidth, box)
				ah := assetSize(node.AssetHeight, box)
				w = math.Max(aw, label.Width+8)
				h = ah
				if label.Height > 0 {
					h += label.Height + 6
				}
				if node.Pos == "t" {
					dy = -ah / 2
				} else {
					dy = ah / 2
				}
				if label.Height == 0 {
					dy = 0
				}
			} else {
				s := svg.ShapeSizeOf(shape, label.Width, label.Height)
				w, h, dy = s.W, s.H, s.Dy
			}
			view = &nodeView{node: node, style: style, label: label, shape: shape, w: w, h: h, dy: dy}
			c = layout.Cnode(w, h, -1)
		}
		c.Seq = len(views)
		views = append(views, view)
		cnodes = append(cnodes, c)
	}
	for i, node := range graph.Nodes {
		cnodes[i].Parent = -1
		if node.ParentID != nil {
			if p, ok := index[*node.ParentID]; ok {
				cnodes[i].Parent = p
			}
		}
	}

	rootDir := layout.Dir("TB")
	if !turned {
		rootDir = layout.Direction(deref(db.Direction))
	}
	flowsSideways := func(i int) bool {
		var dir layout.Dir
		for p, hops := cnodes[i].Parent, 0; p >= 0 && dir == "" && hops < 10000; p, hops = cnodes[p].Parent, hops+1 {
			dir = cnodes[p].Dir
		}
		if dir == "" {
			dir = rootDir
		}
		return dir == "LR" || dir == "RL"
	}
	// The edges on one side of a node spread along it; a picture or an icon keeps them at its middle.
	for i, view := range views {
		if view.node.IsGroup {
			cnodes[i].Span = math.Inf(1)
		} else if view.node.Img == "" && view.node.Icon == "" {
			cnodes[i].Span = svg.ShapeSpan(view.shape, view.w, view.h, flowsSideways(i))
		}
	}

	var cedges []*layout.CEdge
	var edgeLabels []text.Label
	var drawn []*GraphEdge
	for _, edge := range graph.Edges {
		src, ok1 := index[edge.Start]
		dst, ok2 := index[edge.End]
		if !ok1 || !ok2 {
			continue
		}
		label := text.LayoutLabel(edge.Label, edge.LabelType == "markdown", measurer, edgeSize, wrapWidth, 0)
		if src == dst {
			// A self-loop sits beside the node, across the flow, and the node's box grows to hold it.
			views[src].loops++
			if flowsSideways(src) {
				extra := 0.0
				if label.Height > 0 {
					extra = label.Height + 8
				}
				cnodes[src].H += 2 * (LOOP + extra)
			} else {
				extra := 0.0
				if label.Width > 0 {
					extra = label.Width + 12
				}
				cnodes[src].W += 2 * (LOOP + extra)
			}
		}
		drawn = append(drawn, edge)
		edgeLabels = append(edgeLabels, label)
		minlen := 1
		if edge.Minlen != nil {
			minlen = *edge.Minlen
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

	nodeSep := numberOption(flow, "nodeSpacing", NODE_SEP)
	rankSep := numberOption(flow, "rankSpacing", RANK_SEP)
	if tight {
		nodeSep *= 0.5
		rankSep *= 0.75
	}
	curveStr, isString := flow["curve"].(string)
	layoutOptions := layout.LayeredOptions{
		NodeSep:  nodeSep,
		EdgeSep:  EDGE_SEP,
		RankSep:  rankSep,
		PortSep:  20,
		Tracks:   isString && reStep.MatchString(curveStr),
		Straight: isString && curveStr == "linear",
		HeadRoom: svg.MarkerTrim("arrow_point"),
	}
	var lay layout.CompoundResult
	if variant != nil {
		lay = variant.Layout(cnodes, cedges, rootDir, layoutOptions, tight)
	} else {
		lay = layout.CompoundLayout(cnodes, cedges, rootDir, layoutOptions)
	}

	titleMax := 4000.0
	if tight {
		titleMax = common.TitleRoom(options)
	}
	title := text.LayoutLabel(db.Title, false, measurer, size, titleMax, text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 12
	}
	inner := math.Max(lay.Width, title.Width)
	ox := pad + (inner-lay.Width)/2
	oy := pad + titleHeight
	layout.ShiftLayout(cnodes, cedges, ox, oy)
	width := math.Ceil(inner + 2*pad)
	height := math.Ceil(lay.Height + titleHeight + 2*pad)
	links := []types.LinkInfo{}

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
	}
	parents := make([]int, len(cnodes))
	for i, c := range cnodes {
		parents[i] = c.Parent
	}

	var clusters, struckTitles, nodesOut strings.Builder
	for i, view := range views {
		c := cnodes[i]
		node := view.node
		x := c.X
		y := c.Y
		classes := svg.ClassNames(reDefaultClass.ReplaceAllString(node.CssClasses, ""))
		id := svg.EscText(node.ID)
		if c.IsGroup && variant != nil && c.Parent < 0 {
			if out, ok := variant.DrawGroup(c, view.label, view.style, classes, id, rootDir, icons); ok {
				clusters.WriteString(out)
				continue
			}
		}
		if c.IsGroup {
			titleX := svg.ClusterTitleX(c, view.label.Width, crossings, groupPad)
			titleY := y - c.H/2 + 8 + view.label.Height/2
			titleAttrs := ` class="pele-cluster-label" fill="var(--_m)"` + view.style.Text
			struck := svg.TitleCrossed(c, view.label.Width, titleX, crossings)
			if struck {
				struckTitles.WriteString(svg.StruckTitle(view.label, titleX, titleY, titleAttrs, id, parents, i, icons))
			}
			clusters.WriteString(`<g class="pele-cluster` + classes + `" data-id="` + id + `">` +
				`<rect x="` + svg.Num(x-c.W/2) + `" y="` + svg.Num(y-c.H/2) + `" width="` + svg.Num(c.W) + `" height="` + svg.Num(c.H) +
				`" rx="` + svg.RADIUS + `" fill="var(--_a)" fill-opacity="0.5" stroke="var(--_b)"` + view.style.Shape + `/>`)
			if !struck {
				clusters.WriteString(svg.LabelSvg(view.label, titleX, titleY, titleAttrs, icons))
			}
			clusters.WriteString(`</g>`)
			continue
		}

		w := view.w
		h := view.h
		var innerSvg string
		if node.Img != "" {
			aw := assetSize(node.AssetWidth, 80)
			ah := assetSize(node.AssetHeight, 80)
			top := -ah / 2
			if view.label.Height > 0 {
				if node.Pos == "t" {
					top = h/2 - ah
				} else {
					top = -h / 2
				}
			}
			if src, ok := util.ImageUrl(util.SafeUrl(node.Img), options); ok {
				aspect := "none"
				if node.Constraint == "on" {
					aspect = "xMidYMid meet"
				}
				innerSvg = `<image href="` + svg.Esc(src) + `" x="` + svg.Num(-aw/2) + `" y="` + svg.Num(top) + `" width="` + svg.Num(aw) + `" height="` + svg.Num(ah) +
					`" preserveAspectRatio="` + aspect + `"/>`
			}
		} else if node.Icon != "" {
			box := assetSize(node.AssetHeight, 48)
			top := -box / 2
			if view.label.Height > 0 {
				if node.Pos == "t" {
					top = h/2 - box
				} else {
					top = -h / 2
				}
			}
			frame := ""
			switch node.Shape {
			case "icon":
			case "iconCircle":
				frame = `<circle cy="` + svg.Num(top+box/2) + `" r="` + svg.Num(box/2) + `"` + SHAPE_ATTRS + view.style.Shape + `/>`
			default:
				rx := "0"
				if node.Shape == "iconRounded" {
					rx = "8"
				}
				frame = `<rect x="` + svg.Num(-box/2) + `" y="` + svg.Num(top) + `" width="` + svg.Num(box) + `" height="` + svg.Num(box) + `" rx="` + rx + `"` + SHAPE_ATTRS + view.style.Shape + `/>`
			}
			glyph := box * 0.6
			markup := ""
			if icons != nil {
				markup = icons(node.Icon)
			}
			innerSvg = frame + `<svg class="pele-icon" data-icon="` + svg.EscText(node.Icon) + `" x="` + svg.Num(-glyph/2) + `" y="` + svg.Num(top+(box-glyph)/2) +
				`" width="` + svg.Num(glyph) + `" height="` + svg.Num(glyph) + `" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + markup + `</svg>`
		} else {
			innerSvg = svg.DrawShape(view.shape, w, h, SHAPE_ATTRS+view.style.Shape, LINE_ATTRS+view.style.Line)
		}

		label := svg.LabelSvg(view.label, 0, view.dy, ` class="pele-label"`+view.style.Text, icons)
		internal := reInternalLink.MatchString(classes + " ")
		if internal && deref(node.Label) != "" && !(options.Links != nil && !*options.Links) {
			// The label is a note name, but it still ends up in an href, so it gets the same check as a URL.
			name := text.DecodeEntities(*node.Label)
			_, safe := util.LinkUrl(util.SanitizeUrl(name), options)
			target := svg.Esc(name)
			href := ""
			if safe {
				href = ` href="` + target + `"` + util.RelAttr(options)
			}
			label = `<a class="internal-link"` + href + ` data-href="` + target + `">` + label + `</a>`
			links = append(links, types.LinkInfo{ID: node.ID, Href: name, Internal: true})
		}
		body := innerSvg + label
		if node.Tooltip != nil && *node.Tooltip != "" {
			body = `<title>` + svg.EscText(*node.Tooltip) + `</title>` + body
		}
		if node.Link != nil && *node.Link != "" {
			attrs := ""
			if node.LinkTarget != nil && *node.LinkTarget != "" {
				attrs = ` target="` + svg.Esc(*node.LinkTarget) + `"`
			}
			body = util.Linked(body, *node.Link, node.ID, &links, options, attrs)
		}
		nodesOut.WriteString(`<g class="pele-node pele-shape-` + view.shape + classes + `" data-id="` + id + `" transform="translate(` + svg.Num(x) + `,` + svg.Num(y) + `)">` + body + `</g>`)
	}

	reach := svg.NewSideReach()
	for i, ce := range cedges {
		if ce.Src == ce.Dst || deref(drawn[i].Thickness) == "invisible" || len(ce.Route) < 6 {
			continue
		}
		reach.Add(ce.Src, svg.EndSide(ce.Route, 0, cnodes[ce.Src]), drawn[i].ArrowTypeStart)
		reach.Add(ce.Dst, svg.EndSide(ce.Route, len(ce.Route)-3, cnodes[ce.Dst]), drawn[i].ArrowTypeEnd)
	}

	var edgesOut, labelsOut strings.Builder
	loopSeen := map[int]int{}
	for i, ce := range cedges {
		edge := drawn[i]
		label := edgeLabels[i]
		var style *svg.ResolvedStyle
		if len(edge.Style) > 0 || len(edge.CssCompiledStyles) > 0 {
			s := svg.ResolveStyle(append(append([]string{}, edge.CssCompiledStyles...), edge.Style...))
			style = &s
		}
		invisible := deref(edge.Thickness) == "invisible"
		if !invisible {
			startType := edge.ArrowTypeStart
			endType := edge.ArrowTypeEnd
			var path svg.EdgePath
			if ce.Src == ce.Dst {
				view := views[ce.Src]
				k := loopSeen[ce.Src]
				loopSeen[ce.Src] = k + 1
				ll := svg.LoopLabel{LabelW: ce.LabelW, LabelH: ce.LabelH, LabelX: ce.LabelX, LabelY: ce.LabelY}
				c := cnodes[ce.Src]
				path = svg.LoopPath(c.X, c.Y, view.w, view.h, k, flowsSideways(ce.Src), LOOP, svg.MarkerTrim(endType), &ll)
				ce.LabelX, ce.LabelY = ll.LabelX, ll.LabelY
			} else {
				route := append([]float64{}, ce.Route...)
				leaves := reach.Of(ce.Src, svg.EndSide(route, 0, cnodes[ce.Src]))
				arrives := reach.Of(ce.Dst, svg.EndSide(route, len(route)-3, cnodes[ce.Dst]))
				svg.InsetRoute(route, 0, svg.ShapeView{Shape: views[ce.Src].shape, W: views[ce.Src].w, H: views[ce.Src].h}, cnodes[ce.Src])
				svg.InsetRoute(route, len(route)-3, svg.ShapeView{Shape: views[ce.Dst].shape, W: views[ce.Dst].w, H: views[ce.Dst].h}, cnodes[ce.Dst])
				path = svg.RoutePath(route, edge.Curve, svg.MarkerTrim(startType), svg.MarkerTrim(endType), leaves, arrives)
			}
			color := "var(--_l)"
			if style != nil && style.Stroke != "" {
				color = svg.Esc(style.Stroke)
			}
			attrs := ""
			switch deref(edge.Thickness) {
			case "thick":
				attrs += ` stroke-width="2.5"`
			case "dotted":
				attrs += ` stroke-dasharray="3 4"`
			}
			classes := svg.ClassNames(reEdgeClasses.ReplaceAllString(edge.Classes, ""))
			line := ""
			if style != nil {
				line = style.Line
			}
			edgesOut.WriteString(`<g class="pele-edge` + classes + `" data-id="` + svg.EscText(edge.ID) + `"` + line + `>` +
				`<path d="` + path.D + `"` + attrs + `/>` +
				svg.Marker(startType, path.Sx, path.Sy, path.Sdx, path.Sdy, color) +
				svg.Marker(endType, path.Ex, path.Ey, path.Edx, path.Edy, color) +
				`</g>`)
		}
		if label.Width > 0 {
			textAttrs := ""
			if style != nil {
				textAttrs = style.Text
			}
			labelsOut.WriteString(svg.EdgeLabelSvg(edge.ID, label, ce.LabelX, ce.LabelY, textAttrs, icons))
		}
	}

	typ := "flowchart"
	if variant != nil {
		typ = variant.Type()
	}
	content := svg.LabelSvg(title, width/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil)
	if clusters.Len() > 0 {
		content += `<g class="pele-clusters">` + clusters.String() + `</g>`
	}
	if edgesOut.Len() > 0 {
		content += `<g class="pele-edges" fill="none" stroke="var(--_l)" stroke-linecap="round">` + edgesOut.String() + `</g>`
	}
	if struckTitles.Len() > 0 {
		content += `<g class="pele-cluster-titles">` + struckTitles.String() + `</g>`
	}
	if labelsOut.Len() > 0 {
		content += `<g class="pele-edge-labels" font-size="` + js.Num(edgeSize) + `">` + labelsOut.String() + `</g>`
	}
	content += `<g class="pele-nodes">` + nodesOut.String() + `</g>`
	out := svg.SvgDocument(typ, width, height, size, options, svg.Accessible{AccTitle: db.AccTitle, AccDescr: db.AccDescr}, content)

	rendered := types.Rendered{SVG: out, Width: width, Height: height, Links: links}
	if options.Geometry != nil {
		g := &types.Geometry{Width: width, Height: height}
		for i, view := range views {
			c := cnodes[i]
			n := types.GeoNode{ID: view.node.ID, Shape: view.shape, X: c.X, Y: c.Y, W: view.w, H: view.h, Label: view.label.Plain(), Group: c.IsGroup, Parent: c.Parent}
			if c.IsGroup {
				n.W, n.H = c.W, c.H
			}
			g.Nodes = append(g.Nodes, n)
		}
		for i, ce := range cedges {
			stroke := deref(drawn[i].Thickness)
			if stroke == "" {
				stroke = "normal"
			}
			g.Edges = append(g.Edges, types.GeoEdge{From: ce.Src, To: ce.Dst, Route: append([]float64{}, ce.Route...), Start: drawn[i].ArrowTypeStart, End: drawn[i].ArrowTypeEnd,
				Stroke: stroke, Label: edgeLabels[i].Plain(), LabelX: ce.LabelX, LabelY: ce.LabelY})
		}
		rendered.Geometry = g
	}
	return rendered
}
