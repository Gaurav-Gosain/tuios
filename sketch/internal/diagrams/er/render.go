// Ported from pele src/diagrams/er/render.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package er

import (
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/layout"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
	"tuios.dev/sketch/internal/util"
)

const (
	NODE_SEP     = 48
	EDGE_SEP     = 20
	RANK_SEP     = 64
	GROUP_PAD    = 20
	LOOP         = 28
	LOOP_BAND    = 38
	MIN_WIDTH    = 96
	BOX_PAD_X    = 18
	BOX_PAD_Y    = 12
	HEAD_PAD_Y   = 9
	CELL_PAD_X   = 12
	CELL_GAP     = 16
	ROW_PAD      = 9
	COMMENT_WRAP = 260
	NO_WRAP      = 1e6
	PORT_GAP     = 18
	PORT_MARGIN  = 14
	BOX_ATTRS    = ` fill="var(--_s)" stroke="var(--_b)"`
)

func numberOption(config types.Config, key string, fallback float64) float64 {
	conf, _ := config["er"].(map[string]any)
	if value, ok := conf[key].(float64); ok && value > 0 {
		return math.Min(value, 2000)
	}
	return fallback
}

// Type, name, keys, comment.
type Cells = [4]text.Label

type Row struct {
	cells Cells
	h     float64
}

type NodeView struct {
	node  *ErGraphNode
	style svg.ResolvedStyle
	label text.Label
	w     float64
	h     float64
	headH float64
	rows  []Row
	// Left edge of each column, measured from the left edge of the box.
	cols    []float64
	rowSize float64
	keySize float64
	// Whether self-relationships loop out of the bottom side rather than the right.
	below bool
}

// A relationship from an entity to itself, with the stretch of the side it loops out of.
type Loop struct {
	label text.Label
	band  float64
	at    float64
}

// Writes a measured label starting at x, with the baseline of its first line at y.
func textLeft(label text.Label, x, y float64, attrs string, icons svg.IconResolver) string {
	num := svg.Num
	lines := label.Lines
	if len(lines) == 0 {
		return ""
	}
	if len(lines) == 1 && len(lines[0]) == 1 && lines[0][0].Icon == "" && lines[0][0].Style == 0 {
		return `<text` + attrs + ` x="` + num(x) + `" y="` + num(y) + `">` + svg.Esc(lines[0][0].Text) + `</text>`
	}
	shared := svg.SharedStyle(label)
	var body, extra strings.Builder
	for _, line := range lines {
		hasIcon := false
		for _, span := range line {
			if span.Icon != "" {
				hasIcon = true
			}
		}
		sx := x
		placed := false
		for _, span := range line {
			if span.Icon != "" {
				side := label.Size * 1.1
				inner := ""
				if icons != nil {
					inner = icons(span.Icon)
				}
				extra.WriteString(`<svg class="pele-icon" data-icon="` + svg.EscText(span.Icon) + `" x="` + num(sx) + `" y="` + num(y-label.Size*0.35-side/2) +
					`" width="` + num(side) + `" height="` + num(side) + `" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + inner + `</svg>`)
			} else if span.Text != "" {
				pos := ""
				if hasIcon || !placed {
					pos = ` x="` + num(sx) + `" y="` + num(y) + `"`
				}
				body.WriteString(`<tspan` + pos + svg.SpanStyle(span.Style&^shared) + `>` + svg.Esc(span.Text) + `</tspan>`)
				placed = true
			}
			sx += span.Width
		}
		y += label.LineHeight
	}
	return `<text` + svg.TextStyle(attrs, shared) + ` xml:space="preserve">` + body.String() + `</text>` + extra.String()
}

// Mermaid reads `~` pairs as angle brackets. They are written as entities so the label parser
// does not take `<int>` for a tag.
func cellText(raw string) string {
	converted := common.ParseGenericTypes(raw)
	if converted == raw {
		return raw
	}
	return strings.ReplaceAll(strings.ReplaceAll(converted, "<", "&lt;"), ">", "&gt;")
}

// stripDefault is cssClasses.replace(/^default\s?/, ”).
func stripDefault(s string) string {
	if !strings.HasPrefix(s, "default") {
		return s
	}
	s = s[len("default"):]
	if r, size := utf8.DecodeRuneInString(s); size > 0 && r != utf8.RuneError && js.IsSpace(r) {
		s = s[size:]
	}
	return s
}

func renderEr(db *ErDb, config types.Config, options types.RenderOptions) types.Rendered {
	if !common.RunsAcross(db.direction) {
		return draw(db, config, options, false)
	}
	return common.TurnToFit(options, func(down bool) types.Rendered { return draw(db, config, options, down) })
}

// draw: `turned` draws a diagram that runs across as one that runs down.
func draw(db *ErDb, config types.Config, options types.RenderOptions, turned bool) types.Rendered {
	num := svg.Num
	graph := buildErGraph(db)
	size := types.Or(options.FontSize, 16)
	smallSize := js.Round(size * 0.875)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, "")
	}
	pad := types.Or(options.Padding, 8)
	icons := options.Icons

	index := map[string]int{}
	views := make([]*NodeView, 0, len(graph.nodes))
	cnodes := make([]*layout.CNode, 0, len(graph.nodes))
	for i, node := range graph.nodes {
		index[node.id] = i
	}

	var cedges []*layout.CEdge
	var edgeLabels []text.Label
	var drawn []*ErGraphEdge
	loops := map[int][]*Loop{}
	var loopKeys []int
	var loopOf []*Loop
	for _, edge := range graph.edges {
		src, ok := index[edge.start]
		if !ok {
			continue
		}
		dst, ok := index[edge.end]
		if !ok {
			continue
		}
		label := text.LayoutLabel(edge.label, true, measurer, smallSize, 200, 0)
		var loop *Loop
		if src == dst {
			loop = &Loop{label: label}
			if _, ok := loops[src]; !ok {
				loopKeys = append(loopKeys, src)
			}
			loops[src] = append(loops[src], loop)
		}
		loopOf = append(loopOf, loop)
		drawn = append(drawn, edge)
		edgeLabels = append(edgeLabels, label)
		labelW := 0.0
		if label.Width > 0 {
			labelW = label.Width + 12
		}
		labelH := 0.0
		if label.Height > 0 {
			labelH = label.Height + 4
		}
		cedges = append(cedges, &layout.CEdge{Src: src, Dst: dst, Minlen: 1, LabelW: labelW, LabelH: labelH, Route: []float64{}})
	}

	// The direction of the level a node is laid out in: its nearest subgraph that sets one, or the diagram's.
	horizontal := func(i int) bool {
		at := i
		// Subgraphs can name each other as members, so the walk up is bounded.
		for steps := 0; steps < len(graph.nodes); steps++ {
			node := graph.nodes[at]
			if !node.hasParent {
				break
			}
			up, ok := index[node.parentId]
			if !ok {
				break
			}
			dir := graph.nodes[up].dir
			if dir != "" {
				return dir == "LR" || dir == "RL"
			}
			at = up
		}
		return db.direction == "LR" || db.direction == "RL"
	}

	for i, node := range graph.nodes {
		decls := node.cssCompiledStyles
		if len(node.cssStyles) > 0 {
			decls = slices.Concat(node.cssCompiledStyles, node.cssStyles)
		}
		style := svg.ResolveStyle(decls)
		base := 0
		if style.Bold {
			base |= text.Bold
		}
		if style.Italic {
			base |= text.Italic
		}
		nameSize := size
		rowSize := smallSize
		keySize := js.Round(size * 0.75)
		if style.FontSize != 0 {
			nameSize, rowSize, keySize = style.FontSize, style.FontSize, style.FontSize
		}
		var label text.Label
		if node.isGroup {
			label = text.LayoutLabel(node.label, true, measurer, nameSize, 4000, base)
		} else {
			name := node.alias
			if name == "" {
				name = node.label
			}
			label = text.LayoutLabel(name, true, measurer, nameSize, 240, base)
		}
		own := loops[i]
		// Loops leave by a side the layout keeps free of other edges: the right when ranks run down,
		// the bottom when they run across.
		below := len(own) > 0 && !node.isGroup && horizontal(i)
		view := &NodeView{node: node, style: style, label: label, rowSize: rowSize, keySize: keySize, below: below}
		reach := 0.0
		bands := 0.0
		for _, loop := range own {
			if below {
				loop.band = math.Max(LOOP_BAND, loop.label.Width+12)
				reach = math.Max(reach, loop.label.Height)
			} else {
				loop.band = math.Max(LOOP_BAND, loop.label.Height+6)
				reach = math.Max(reach, loop.label.Width)
			}
			bands += loop.band
		}
		minW := float64(MIN_WIDTH)
		if below {
			minW = math.Max(MIN_WIDTH, bands+8)
		}
		var c *layout.CNode
		if node.isGroup {
			c = layout.Cnode(0, 0, -1)
			c.IsGroup = true
			if node.dir != "" {
				c.Dir = layout.Direction(node.dir)
			}
			c.PadX = GROUP_PAD
			if view.label.Height > 0 {
				c.PadTop = view.label.Height + 16
			} else {
				c.PadTop = GROUP_PAD
			}
			c.PadBottom = GROUP_PAD
			c.MinW = math.Max(view.label.Width+2*GROUP_PAD-12, MIN_WIDTH)
		} else {
			nameH := math.Max(label.Height, js.Round(nameSize*1.5))
			if len(node.attributes) == 0 {
				view.w = math.Max(label.Width+2*BOX_PAD_X, minW)
				least := 0.0
				if !below {
					least = bands + 8
				}
				view.h = math.Max(nameH+2*BOX_PAD_Y, least)
			} else {
				rowLine := js.Round(rowSize * 1.5)
				widths := [4]float64{}
				for _, attribute := range node.attributes {
					cells := Cells{
						text.LayoutLabel(cellText(attribute.typ), true, measurer, rowSize, NO_WRAP, base),
						text.LayoutLabel(cellText(attribute.name), true, measurer, rowSize, NO_WRAP, base),
						text.LayoutLabel(strings.Join(attribute.keys, ", "), false, measurer, keySize, NO_WRAP, base),
						text.LayoutLabel(cellText(attribute.comment), true, measurer, rowSize, COMMENT_WRAP, base),
					}
					h := rowLine
					for k := 0; k < 4; k++ {
						if cells[k].Width > widths[k] {
							widths[k] = cells[k].Width
						}
						if cells[k].Height > h {
							h = cells[k].Height
						}
					}
					view.rows = append(view.rows, Row{cells: cells, h: h + ROW_PAD})
					view.h += h + ROW_PAD
				}
				columns := 0
				inner := 0.0
				for _, w := range widths {
					if w == 0 {
						continue
					}
					gap := 0.0
					if columns > 0 {
						gap = CELL_GAP
					}
					inner += w + gap
					columns++
				}
				view.w = math.Max(math.Max(inner+2*CELL_PAD_X, label.Width+2*BOX_PAD_X), minW)
				// A box wider than its table shares the spare width between the columns.
				spare := 0.0
				if columns > 0 {
					spare = (view.w - inner - 2*CELL_PAD_X) / float64(columns)
				}
				x := float64(CELL_PAD_X)
				for _, w := range widths {
					view.cols = append(view.cols, x)
					if w > 0 {
						x += w + spare + CELL_GAP
					}
				}
				view.headH = nameH + 2*HEAD_PAD_Y
				view.h += view.headH
			}
			// The layout keeps a node centered in its box, so room for loops is added on both sides.
			room := 0.0
			if len(own) > 0 {
				room = 2 * (MARKER_LENGTH + LOOP*0.75 + 10 + reach)
			}
			if below {
				c = layout.Cnode(view.w, view.h+room, -1)
			} else {
				c = layout.Cnode(view.w+room, view.h, -1)
			}
		}
		c.Seq = len(views)
		views = append(views, view)
		cnodes = append(cnodes, c)
	}
	for i, node := range graph.nodes {
		cnodes[i].Parent = -1
		if node.hasParent {
			if up, ok := index[node.parentId]; ok {
				cnodes[i].Parent = up
			}
		}
	}

	rootDir := layout.Direction(db.direction)
	if turned {
		rootDir = "TB"
	}
	lay := layout.CompoundLayout(cnodes, cedges, rootDir, layout.LayeredOptions{
		NodeSep: numberOption(config, "nodeSpacing", NODE_SEP),
		EdgeSep: EDGE_SEP,
		RankSep: numberOption(config, "rankSpacing", RANK_SEP),
		PortSep: 24,
	})

	spreadEnds(cedges, cnodes, views)

	// A subgraph gets its size from the layout, so its loops hang outside it and may widen the diagram.
	layoutWidth := lay.Width
	for _, i := range loopKeys {
		c := cnodes[i]
		if !c.IsGroup {
			continue
		}
		reach := 0.0
		for _, loop := range loops[i] {
			reach = math.Max(reach, loop.label.Width)
		}
		layoutWidth = math.Max(layoutWidth, c.X+c.W/2+MARKER_LENGTH+LOOP*0.75+10+reach)
	}

	title := text.LayoutLabel(db.title, false, measurer, size, 4000, text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 12
	}
	inner := math.Max(layoutWidth, title.Width)
	ox := pad + (inner-layoutWidth)/2
	oy := pad + titleHeight
	layout.ShiftLayout(cnodes, cedges, ox, oy)
	width := math.Ceil(inner + 2*pad)
	height := math.Ceil(lay.Height + titleHeight + 2*pad)
	var links []types.LinkInfo

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

	var clusters, nodesOut strings.Builder
	for i, view := range views {
		c := cnodes[i]
		node := view.node
		classes := svg.ClassNames(stripDefault(node.cssClasses))
		if c.IsGroup {
			view.w = c.W
			view.h = c.H
			x := c.X - c.W/2
			y := c.Y - c.H/2
			titleX := svg.ClusterTitleX(c, view.label.Width, crossings, GROUP_PAD)
			clusters.WriteString(`<g class="pele-cluster` + classes + `" data-id="` + svg.EscText(node.id) + `">` +
				`<rect x="` + num(x) + `" y="` + num(y) + `" width="` + num(c.W) + `" height="` + num(c.H) + `" rx="` + svg.RADIUS + `" fill="var(--_a)" fill-opacity="0.5" stroke="var(--_b)"` + view.style.Shape + `/>` +
				svg.LabelSvg(view.label, titleX, y+8+view.label.Height/2, ` class="pele-cluster-label" fill="var(--_m)"`+view.style.Text, icons) +
				`</g>`)
			continue
		}

		w := view.w
		h := view.h
		x0 := -w / 2
		y0 := -h / 2
		txt := view.style.Text
		shape := `x="` + num(x0) + `" y="` + num(y0) + `" width="` + num(w) + `" height="` + num(h) + `" rx="` + svg.RADIUS + `"`
		nameY := 0.0
		if len(view.rows) > 0 {
			nameY = y0 + view.headH/2
		}
		name := svg.LabelSvg(view.label, 0, nameY, ` class="pele-label"`+txt, icons)
		display := node.alias
		if display == "" {
			display = node.label
		}
		display = text.DecodeEntities(display)
		if strings.Contains(classes+" ", " internal-link ") && name != "" && (options.Links == nil || *options.Links) {
			// The name ends up in an href, so it gets the same check as a URL.
			_, safe := util.LinkUrl(util.SanitizeUrl(display), options)
			target := svg.Esc(display)
			href := ""
			if safe {
				href = ` href="` + target + `"` + util.RelAttr(options)
			}
			name = `<a class="internal-link"` + href + ` data-href="` + target + `">` + name + `</a>`
			links = append(links, types.LinkInfo{ID: node.label, Href: display, Internal: true})
		}

		var body string
		if len(view.rows) == 0 {
			body = `<rect ` + shape + BOX_ATTRS + view.style.Shape + `/>` + name
		} else {
			rowLine := js.Round(view.rowSize * 1.5)
			lineColor := "var(--_b)"
			if view.style.Stroke != "" {
				lineColor = svg.Esc(view.style.Stroke)
			}
			var stripes, cells strings.Builder
			y := y0 + view.headH
			for r, row := range view.rows {
				if r%2 == 0 {
					span := `x="` + num(x0) + `" width="` + num(w) + `"`
					if r == len(view.rows)-1 {
						// The last row follows the box's rounded bottom corners: a rounded rect, squared off on top.
						stripes.WriteString(`<rect ` + span + ` y="` + num(y) + `" height="` + num(row.h) + `" rx="` + svg.RADIUS + `"/>` +
							`<rect ` + span + ` y="` + num(y) + `" height="` + num(row.h/2) + `"/>`)
					} else {
						stripes.WriteString(`<rect ` + span + ` y="` + num(y) + `" height="` + num(row.h) + `"/>`)
					}
				}
				baseline := y + ROW_PAD/2.0 + rowLine/2 + view.rowSize*0.35
				cells.WriteString(textLeft(row.cells[0], x0+view.cols[0], baseline, ` class="pele-er-type" fill="var(--_m)"`+txt, icons) +
					textLeft(row.cells[1], x0+view.cols[1], baseline, ` class="pele-er-name"`+txt, icons) +
					textLeft(row.cells[2], x0+view.cols[2], baseline, ` class="pele-er-keys" font-size="`+js.Num(view.keySize)+`" fill="var(--_m)"`+txt, icons) +
					textLeft(row.cells[3], x0+view.cols[3], baseline, ` class="pele-er-comment" fill="var(--_m)"`+txt, icons))
				y += row.h
			}
			dividerY := num(y0 + view.headH)
			opacity := ""
			if strings.Contains(view.style.Shape, "fill:") {
				opacity = ` opacity="0.6"`
			}
			body = `<rect ` + shape + BOX_ATTRS + view.style.Shape + `/>` +
				// Over a fill the diagram chose, the shading is a tint of that fill. The opacity is on the
				// group because the two parts of the last row overlap.
				`<g class="pele-er-stripes" fill="var(--_bg)"` + opacity + `>` + stripes.String() + `</g>` +
				`<path class="pele-er-divider" d="M` + num(x0) + `,` + dividerY + `H` + num(x0+w) + `" stroke="` + lineColor + `"/>` +
				`<rect ` + shape + ` fill="none" stroke="var(--_b)"` + view.style.Line + `/>` +
				name +
				`<g class="pele-er-attributes" font-size="` + js.Num(view.rowSize) + `">` + cells.String() + `</g>`
		}
		nodesOut.WriteString(`<g class="pele-node pele-entity` + classes + `" data-id="` + svg.EscText(node.label) + `" transform="translate(` + num(c.X) + `,` + num(c.Y) + `)">` + body + `</g>`)
	}

	// Self-relationships share the side they loop out of, squeezed together if it is too short for them.
	for _, i := range loopKeys {
		own := loops[i]
		view := views[i]
		total := 0.0
		for _, loop := range own {
			total += loop.band
		}
		side := view.h
		if view.below {
			side = view.w
		}
		scale := math.Min(1, math.Max(side-8, 0)/total)
		at := (-total * scale) / 2
		for _, loop := range own {
			loop.band *= scale
			loop.at = at + loop.band/2
			at += loop.band
		}
	}

	var edgesOut, labelsOut strings.Builder
	for i, ce := range cedges {
		edge := drawn[i]
		label := edgeLabels[i]
		var d, start, end string
		if loop := loopOf[i]; loop != nil {
			c := cnodes[ce.Src]
			view := views[ce.Src]
			spread := math.Max(math.Min(loop.band/2-6, 13), 1)
			far := MARKER_LENGTH + LOOP*0.75 + 6
			if view.below {
				x := c.X + loop.at
				y := c.Y + view.h/2
				turn := num(y + MARKER_LENGTH)
				out := num(y + MARKER_LENGTH + LOOP)
				d = `M` + num(x-spread) + `,` + num(y) + `V` + turn + `C` + num(x-spread) + `,` + out + ` ` + num(x+spread) + `,` + out + ` ` + num(x+spread) + `,` + turn + `V` + num(y)
				start = erMarker(edge.arrowTypeStart, x-spread, y, 0, -1)
				end = erMarker(edge.arrowTypeEnd, x+spread, y, 0, -1)
				ce.LabelX = x
				ce.LabelY = y + far + label.Height/2
			} else {
				x := c.X + view.w/2
				y := c.Y + loop.at
				turn := num(x + MARKER_LENGTH)
				out := num(x + MARKER_LENGTH + LOOP)
				d = `M` + num(x) + `,` + num(y-spread) + `H` + turn + `C` + out + `,` + num(y-spread) + ` ` + out + `,` + num(y+spread) + ` ` + turn + `,` + num(y+spread) + `H` + num(x)
				start = erMarker(edge.arrowTypeStart, x, y-spread, -1, 0)
				end = erMarker(edge.arrowTypeEnd, x, y+spread, -1, 0)
				ce.LabelX = x + far + label.Width/2
				ce.LabelY = y
			}
		} else {
			route := straighten(ce.Route)
			if len(route) < 6 {
				continue
			}
			inset(route, 0, views[ce.Src], cnodes[ce.Src])
			inset(route, len(route)-3, views[ce.Dst], cnodes[ce.Dst])
			// A curve stops short of each box so that the marker sits on a straight run of line.
			dx := math.Abs(route[len(route)-3] - route[0])
			dy := math.Abs(route[len(route)-2] - route[1])
			direct := len(route) == 6 && (dx < 0.01 || dy < 0.01 || js.Hypot(dx, dy) <= 3*MARKER_LENGTH)
			stub := 0.0
			if !direct {
				stub = MARKER_LENGTH
			}
			path := svg.RoutePath(route, "", stub, stub, svg.DefaultReach(stub), svg.DefaultReach(stub))
			if stub > 0 {
				d = `M` + num(path.Sx) + `,` + num(path.Sy) + `L` + path.D[1:] + `L` + num(path.Ex) + `,` + num(path.Ey)
			} else {
				d = path.D
			}
			start = erMarker(edge.arrowTypeStart, path.Sx, path.Sy, path.Sdx, path.Sdy)
			end = erMarker(edge.arrowTypeEnd, path.Ex, path.Ey, path.Edx, path.Edy)
		}
		dashed := edge.pattern == "dashed"
		kind := "identifying"
		dash := ""
		if dashed {
			kind = "non-identifying"
			dash = ` stroke-dasharray="4 3"`
		}
		edgesOut.WriteString(`<g class="pele-edge pele-er-` + kind + `" data-id="` + svg.EscText(edge.id) + `">` +
			`<path d="` + d + `"` + dash + `/>` +
			start +
			end +
			`</g>`)
		if label.Width > 0 {
			labelsOut.WriteString(svg.EdgeLabelSvg(edge.id, label, ce.LabelX, ce.LabelY, "", icons))
		}
	}

	var content strings.Builder
	content.WriteString(svg.LabelSvg(title, width/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil))
	if clusters.Len() > 0 {
		content.WriteString(`<g class="pele-clusters">` + clusters.String() + `</g>`)
	}
	if edgesOut.Len() > 0 {
		content.WriteString(`<g class="pele-edges" fill="none" stroke="var(--_l)" stroke-linecap="round">` + edgesOut.String() + `</g>`)
	}
	if labelsOut.Len() > 0 {
		content.WriteString(`<g class="pele-edge-labels" font-size="` + js.Num(smallSize) + `">` + labelsOut.String() + `</g>`)
	}
	content.WriteString(`<g class="pele-nodes">` + nodesOut.String() + `</g>`)
	out := svg.SvgDocument("er", width, height, size, options, svg.Accessible{AccTitle: db.accTitle, AccDescr: db.accDescr}, content.String())

	return types.Rendered{SVG: out, Width: width, Height: height, Links: links}
}

func straighten(route []float64) []float64 {
	out := slices.Clone(route[:min(3, len(route))])
	for k := 3; k < len(route); k += 3 {
		prev := len(out) - 3
		flat := func(axis int) bool {
			return math.Abs(out[prev+axis]-route[k+axis]) < 0.01 && math.Abs(route[k+axis]-route[k+3+axis]) < 0.01
		}
		if k+3 < len(route) && (flat(0) || flat(1)) {
			continue
		}
		out = append(out, route[k], route[k+1], route[k+2])
	}
	return out
}

type End struct {
	route  []float64
	at     int
	toward float64
}

// The layout brings every edge that ends on one side of a box to the middle of that side, where
// their markers would cover each other. This spaces them along the side, in the order they fan out.
func spreadEnds(cedges []*layout.CEdge, cnodes []*layout.CNode, views []*NodeView) {
	sides := map[int][]*End{}
	var keys []int
	add := func(node int, route []float64, at int, next int) {
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
			return
		}
		key := node*4 + side
		end := &End{route: route, at: at, toward: route[next+(side&1)]}
		if _, ok := sides[key]; !ok {
			keys = append(keys, key)
		}
		sides[key] = append(sides[key], end)
	}
	for _, e := range cedges {
		last := len(e.Route) - 3
		if e.Src == e.Dst || last < 3 {
			continue
		}
		add(e.Src, e.Route, 0, 3)
		add(e.Dst, e.Route, last, last-3)
	}
	for _, key := range keys {
		ends := sides[key]
		if len(ends) < 2 {
			continue
		}
		node := key >> 2
		vertical := key&1 == 1
		var length float64
		if cnodes[node].IsGroup {
			length = cnodes[node].W
			if vertical {
				length = cnodes[node].H
			}
		} else {
			length = views[node].w
			if vertical {
				length = views[node].h
			}
		}
		step := math.Max(math.Min(PORT_GAP, (length-2*PORT_MARGIN)/float64(len(ends)-1)), 0)
		center := cnodes[node].X
		axis := 0
		if vertical {
			center = cnodes[node].Y
			axis = 1
		}
		slices.SortStableFunc(ends, func(a, b *End) int {
			d := a.toward - b.toward
			if d < 0 {
				return -1
			}
			if d > 0 {
				return 1
			}
			return 0
		})
		for i, end := range ends {
			end.route[end.at+axis] = center + (float64(i)-float64(len(ends)-1)/2)*step
		}
	}
}

// An entity with a self-relationship is laid out larger than it is drawn; this moves a route end
// from the side of that larger box onto the entity's own side.
func inset(route []float64, at int, view *NodeView, c *layout.CNode) {
	if c.IsGroup {
		return
	}
	dx := route[at] - c.X
	dy := route[at+1] - c.Y
	if c.W != view.w && math.Abs(math.Abs(dx)-c.W/2) < 0.5 {
		route[at] -= (js.Sign(dx) * (c.W - view.w)) / 2
	}
	if c.H != view.h && math.Abs(math.Abs(dy)-c.H/2) < 0.5 {
		route[at+1] -= (js.Sign(dy) * (c.H - view.h)) / 2
	}
}
