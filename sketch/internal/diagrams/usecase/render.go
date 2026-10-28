// Ported from pele src/diagrams/usecase/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package usecase

import (
	"math"
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
	NODE_SEP    = 32
	EDGE_SEP    = 16
	RANK_SEP    = 72
	GROUP_PAD   = 20
	LOOP        = 26
	FIGURE_W    = 40
	FIGURE_H    = 60
	FIGURE_GAP  = 6
	TAB_PAD     = 6
	CELL_PAD    = 10
	ROW_PAD     = 8
	FOLD        = 9
	EXTENSION   = 12
	PORT_GAP    = 14
	SHAPE_ATTRS = ` fill="var(--_s)" stroke="var(--_b)"`
)

const (
	kActor = iota
	kEllipse
	kBox
	kJson
	kBoundary
)

type View struct {
	node       *UsecaseGraphNode
	kind       int
	style      svg.ResolvedStyle
	label      text.Label
	stereotype text.Label
	// The size of what is drawn; the layout box is larger when the node carries a self-relationship.
	w     float64
	h     float64
	rows  [][2]text.Label
	keyW  float64
	headH float64
	loops int
}

func numberOption(config types.Config, key string, fallback float64) float64 {
	section, _ := config["usecase"].(map[string]any)
	if value, ok := section[key].(float64); ok && value > 0 {
		return math.Min(value, 2000)
	}
	return fallback
}

// literal: a plain label is shown as written: no tags, no markdown, no `\n` breaks. Its markup characters
// are handed to the label parser as character references, which it turns back into characters.
func literal(s string) string {
	if !strings.ContainsAny(s, `&<>\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '&', '<', '>', '\\':
			b.WriteString("&#" + js.Int(int(c)) + ";")
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func textLabel(raw string, markdown bool, measurer text.TextMeasurer, size, wrap float64, base int) text.Label {
	if raw != "" && !markdown {
		raw = literal(raw)
	}
	return text.LayoutLabel(raw, markdown, measurer, size, wrap, base)
}

// anchor is the point of a node's outline on one of its four sides (0 top, 1 right, 2 bottom, 3 left),
// `along` away from the middle of that side. Lines meet an actor at its figure, not at its label.
func anchor(view *View, c *layout.CNode, side int, along float64) (float64, float64) {
	sign := -1.0
	if side == 1 || side == 2 {
		sign = 1
	}
	upright := side == 1 || side == 3
	reach := view.h / 2
	if upright {
		reach = view.w / 2
	}
	middle := 0.0
	if view.kind == kActor {
		if upright {
			reach = FIGURE_W/2 + 3
			middle = (FIGURE_H - view.h) / 2
		} else {
			reach += 2
		}
	} else if view.kind == kEllipse {
		span := view.w / 2
		if upright {
			span = view.h / 2
		}
		q := along / span
		reach *= math.Sqrt(math.Max(1-float64(q*q), 0))
	}
	if upright {
		return c.X + float64(sign*reach), c.Y + middle + along
	}
	return c.X + along, c.Y + float64(sign*reach)
}

// sideLength is how much of a side edges may spread over.
func sideLength(view *View, side int) float64 {
	upright := side == 1 || side == 3
	if view.kind == kActor {
		if upright {
			return float64(FIGURE_H) * 0.5
		}
		return float64(FIGURE_W) * 0.6
	}
	length := view.w
	if upright {
		length = view.h
	}
	if view.kind == kEllipse {
		return length * 0.6
	}
	return length - 16
}

type End struct {
	// key is the node and side: index*4 + side.
	key    int
	route  []float64
	at     int
	toward float64
}

// attachEnds: the layout brings every edge that ends on one side of a node to the middle of that side. This
// spaces them along the side in the order they fan out, and puts each on the node's outline.
func attachEnds(cedges []*layout.CEdge, cnodes []*layout.CNode, views []View) {
	// The ends of all sides in one list, sorted by side and then, stably, by where they head;
	// pele keeps a list for each side and sorts each.
	ends := make([]End, 0, 2*len(cedges))
	add := func(index int, route []float64, at int, next int) {
		c := cnodes[index]
		if c.IsGroup {
			return
		}
		dx := route[at] - c.X
		dy := route[at+1] - c.Y
		var side int
		switch {
		case math.Abs(math.Abs(dy)-c.H/2) < 0.5:
			side = 2
			if dy < 0 {
				side = 0
			}
		case math.Abs(math.Abs(dx)-c.W/2) < 0.5:
			side = 1
			if dx < 0 {
				side = 3
			}
		default:
			return
		}
		key := index*4 + side
		toward := route[next]
		if side&1 != 0 {
			toward = route[next+1]
		}
		ends = append(ends, End{key, route, at, toward})
	}
	for _, e := range cedges {
		last := len(e.Route) - 3
		if e.Src == e.Dst || last < 3 {
			continue
		}
		add(e.Src, e.Route, 0, 3)
		add(e.Dst, e.Route, last, last-3)
	}
	js.Sort(ends, func(a, b End) float64 {
		if a.key != b.key {
			return float64(a.key - b.key)
		}
		return a.toward - b.toward
	})
	for from := 0; from < len(ends); {
		to := from + 1
		for to < len(ends) && ends[to].key == ends[from].key {
			to++
		}
		side := ends[from].key & 3
		index := ends[from].key >> 2
		view := &views[index]
		count := to - from
		step := 0.0
		if count > 1 {
			step = math.Min(PORT_GAP, sideLength(view, side)/float64(count-1))
		}
		for i := 0; i < count; i++ {
			end := &ends[from+i]
			x, y := anchor(view, cnodes[index], side, float64((float64(i)-float64(count-1)/2)*step))
			end.route[end.at] = x
			end.route[end.at+1] = y
		}
		from = to
	}
}

// extension is the hollow triangle of a generalization, with its tip at (x, y).
func extension(x, y, dx, dy float64, color string) string {
	bx := x - float64(dx*EXTENSION)
	by := y - float64(dy*EXTENSION)
	half := float64(EXTENSION) * 0.5
	return `<path class="pele-marker" d="M` + svg.Num(x) + `,` + svg.Num(y) + `L` + svg.Num(bx-float64(dy*half)) + `,` + svg.Num(by+float64(dx*half)) +
		`L` + svg.Num(bx+float64(dy*half)) + `,` + svg.Num(by-float64(dx*half)) + `Z" fill="var(--_bg)" stroke="` + color + `" stroke-linejoin="round"/>`
}

func trim(typ string) float64 {
	if typ == "extension" {
		return EXTENSION
	}
	return svg.MarkerTrim(typ)
}

// mark draws a marker; stroke "" leaves the filled markers unstroked.
func mark(typ string, x, y, dx, dy float64, color, stroke string) string {
	if typ == "extension" {
		return extension(x, y, dx, dy, color)
	}
	if stroke == "" {
		stroke = "none"
	}
	return svg.MarkerStroke(typ, x, y, dx, dy, color, stroke)
}

func figure(view *View, icons text.IconResolver) string {
	node := view.node
	shape := view.style.Shape
	line := ` fill="none" stroke="var(--_b)"` + view.style.Line
	head := -21.0
	radius := 9.0
	var out string
	switch node.actorType {
	case "hollow":
		radius = 8
		out = `<circle cy="-21" r="8" fill="var(--_bg)" stroke="var(--_b)"` + shape + `/>` +
			`<path d="M-20,-9H20V0H5.5L20,15.5L12,25.5L0,12L-12,25.5L-20,15.5L-5.5,0H-20Z" fill="var(--_bg)" stroke="var(--_b)" stroke-linejoin="round"` + shape + `/>`
	case "awesome":
		out = `<path d="M0,-30A11,11 0 1 1 0,-8A11,11 0 1 1 0,-30ZM-20,25C-20,9 -12,-3 0,-3C12,-3 20,9 20,25C20,28 18,30 15,30H-15C-18,30 -20,28 -20,25Z" fill="var(--_b)"` + shape + `/>`
	case "icon":
		head = -14
		radius = 12
		icon := ""
		if icons != nil {
			icon = icons(node.icon)
		}
		out = `<rect x="-22" y="-26" width="44" height="44"` + svg.Rounded(SHAPE_ATTRS+shape) + `/>` +
			`<svg class="pele-icon" data-icon="` + svg.EscText(node.icon) + `" x="-14" y="-18" width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` +
			icon + `</svg>`
	default:
		out = `<circle cy="-21" r="9"` + SHAPE_ATTRS + shape + `/><path d="M0,-12V10M-14,-3H14M0,10L-12,30M0,10L12,30"` + line + `/>`
	}
	if node.business {
		// The slash of a business actor: a chord across the lower right of the head.
		out += `<path class="pele-business" d="M` + svg.Num(0.12*radius) + `,` + svg.Num(head+float64(0.99*radius)) + `L` + svg.Num(0.92*radius) + `,` +
			svg.Num(head-float64(0.39*radius)) + `"` + line + `/>`
	}
	return out
}

func renderUsecase(model *UsecaseModel, config types.Config, options types.RenderOptions) types.Rendered {
	if !common.RunsAcross(model.direction) {
		return draw(model, config, options, false)
	}
	return common.TurnToFit(options, func(down bool) types.Rendered { return draw(model, config, options, down) })
}

// trimDefault is classes.replace(/^default\s?/, ”).
func trimDefault(classes string) string {
	if !strings.HasPrefix(classes, "default") {
		return classes
	}
	return dropSpace(classes[len("default"):])
}

// dropSpace drops one whitespace character at the start, as \s? does.
func dropSpace(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	if js.IsSpace(r) {
		return s[size:]
	}
	return s
}

// trimRelationship is classes.replace(/^default relationship relationship-\S+\s?/, ”).
func trimRelationship(classes string) string {
	const prefix = "default relationship relationship-"
	if !strings.HasPrefix(classes, prefix) {
		return classes
	}
	rest := classes[len(prefix):]
	k := 0
	for k < len(rest) {
		r, size := utf8.DecodeRuneInString(rest[k:])
		if js.IsSpace(r) {
			break
		}
		k += size
	}
	if k == 0 {
		return classes
	}
	return dropSpace(rest[k:])
}

// draw: `turned` draws a diagram that runs across as one that runs down.
func draw(model *UsecaseModel, config types.Config, options types.RenderOptions, turned bool) types.Rendered {
	graph := buildUsecaseGraph(model)
	size := types.Or(options.FontSize, 16)
	small := js.Round(size * 0.875)
	tiny := js.Round(size * 0.8125)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)
	icons := options.Icons
	rootDir := layout.Direction(model.direction)
	if turned {
		rootDir = "TB"
	}
	sideways := rootDir == "LR" || rootDir == "RL"
	stereotypeAttrs := ` class="pele-stereotype" font-size="` + js.Num(tiny) + `" fill="var(--_m)"`

	index := make(map[string]int, len(graph.nodes))
	views := make([]View, 0, len(graph.nodes))
	cnodes := make([]*layout.CNode, 0, len(graph.nodes))
	for i, node := range graph.nodes {
		index[node.id] = i
	}

	for k := range graph.nodes {
		node := &graph.nodes[k]
		declarations := node.cssCompiledStyles
		if len(node.cssStyles) > 0 {
			declarations = append(append([]string{}, node.cssCompiledStyles...), node.cssStyles...)
		}
		style := svg.ResolveStyle(declarations)
		base := 0
		if style.Bold {
			base |= text.Bold
		}
		if style.Italic {
			base |= text.Italic
		}
		fontSize := size
		if style.FontSize != 0 {
			fontSize = style.FontSize
		}
		markdown := node.labelType == "markdown"
		stereotypeText := ""
		if node.stereotype != "" {
			stereotypeText = "«" + node.stereotype + "»"
		}
		stereotype := textLabel(stereotypeText, false, measurer, tiny, 200, base)
		views = append(views, View{node: node, kind: kBox, style: style, label: stereotype, stereotype: stereotype})
		view := &views[len(views)-1]
		var c *layout.CNode
		if node.isGroup {
			view.kind = kBoundary
			view.label = textLabel(node.label, markdown, measurer, fontSize, 4000, base)
			titleH := view.label.Height
			c = layout.Cnode(0, 0, -1)
			c.IsGroup = true
			c.PadX = GROUP_PAD
			c.PadBottom = GROUP_PAD
			switch {
			case node.boundaryType == "package":
				c.PadTop = titleH + 2*TAB_PAD + 16
			case titleH > 0:
				c.PadTop = titleH + 16
			default:
				c.PadTop = GROUP_PAD
			}
			c.MinW = view.label.Width + 2*GROUP_PAD
		} else {
			switch {
			case node.isJSON:
				view.kind = kJson
				view.label = textLabel(node.label, false, measurer, fontSize, 4000, base|text.Bold)
				valueW := 0.0
				for _, row := range node.jsonRows {
					key := textLabel(row.key, false, measurer, small, 4000, base)
					value := textLabel(row.value, false, measurer, small, 260, base)
					view.rows = append(view.rows, [2]text.Label{key, value})
					view.keyW = math.Max(view.keyW, key.Width)
					valueW = math.Max(valueW, value.Width)
					view.h += math.Max(math.Max(key.Height, value.Height), js.Round(small*1.5)) + ROW_PAD
				}
				// The root of an empty object has a value and no key, and then there is no key column.
				if view.keyW > 0 {
					view.keyW += 2 * CELL_PAD
				}
				view.headH = math.Max(view.label.Height, js.Round(fontSize*1.5)) + ROW_PAD
				columns := 0.0
				if len(view.rows) > 0 {
					columns = view.keyW + valueW + 2*CELL_PAD
				}
				view.w = math.Max(math.Max(view.label.Width+2*CELL_PAD, columns), 72)
				view.h += view.headH
			case node.actorType != "":
				view.kind = kActor
				view.label = textLabel(node.label, markdown, measurer, fontSize, 140, base)
				view.w = math.Max(math.Max(FIGURE_W, view.label.Width), stereotype.Width)
				view.h = FIGURE_H + FIGURE_GAP + stereotype.Height + view.label.Height
			default:
				labelSize := fontSize
				if node.shape == "note" {
					labelSize = small
				}
				view.label = textLabel(node.label, markdown, measurer, labelSize, 180, base)
				tw := math.Max(view.label.Width, stereotype.Width)
				th := view.label.Height + stereotype.Height
				padding := float64(2 * node.padding)
				switch {
				case node.shape == "usecaseEllipse" || node.shape == "usecaseBusiness":
					view.kind = kEllipse
					// An oval needs more room around its text than a box, and a business oval room for its slash.
					extra := 4.0
					if node.business {
						extra = 16
					}
					view.w = math.Max(tw+padding+extra, 88)
					view.h = math.Max(th+28, 48)
				case node.shape == "note":
					view.w = tw + padding + 6
					view.h = th + 18
				default:
					view.w = math.Max(tw+padding+12, 80)
					view.h = math.Max(th+24, 44)
				}
			}
			c = layout.Cnode(view.w, view.h, -1)
		}
		c.Seq = len(views) - 1
		cnodes = append(cnodes, c)
	}
	for i := range graph.nodes {
		node := &graph.nodes[i]
		cnodes[i].Parent = -1
		if node.parentID != "" {
			if p, ok := index[node.parentID]; ok {
				cnodes[i].Parent = p
			}
		}
	}

	var cedges []*layout.CEdge
	var edgeLabels []text.Label
	drawn := make([]*UsecaseGraphEdge, 0, len(graph.edges))
	edgeLabels = make([]text.Label, 0, len(graph.edges))
	cedges = make([]*layout.CEdge, 0, len(graph.edges))
	for k := range graph.edges {
		edge := &graph.edges[k]
		src, ok1 := index[edge.start]
		dst, ok2 := index[edge.end]
		if !ok1 || !ok2 {
			continue
		}
		semantic := edge.relationshipType == "include" || edge.relationshipType == "extend"
		raw := ""
		if edge.label != nil {
			raw = *edge.label
		}
		if semantic {
			raw = "«" + raw + "»"
		}
		label := textLabel(raw, edge.labelType == "markdown", measurer, small, 200, 0)
		if src == dst {
			// A self-relationship loops out across the flow, and the node's box grows to hold it.
			views[src].loops++
			if sideways {
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
		ce := &layout.CEdge{Src: src, Dst: dst, Minlen: edge.minlen, Route: []float64{}}
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

	title := svg.TitleLabel(types.Or(model.title, ""), measurer, size, 4000)
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

	// Where edges cross each boundary's top border, so that a title can keep out of their way.
	crossings := svg.Crossings{}
	for _, e := range cedges {
		svg.MarkCrossings(crossings, e.Route)
	}
	attachEnds(cedges, cnodes, views)

	var clusters, nodesOut strings.Builder
	var fills []svg.Fill
	for i := range views {
		view := &views[i]
		c := cnodes[i]
		node := view.node
		classes := svg.ClassNames(trimDefault(node.cssClasses))
		id := svg.EscText(node.id)
		textAttrs := view.style.Text
		shape := view.style.Shape
		color := svg.InkFill(view.style, "")
		muted := color
		if muted == "" {
			muted = ` fill="var(--_m)"`
		}
		w := view.w
		h := view.h

		if view.kind == kBoundary {
			x := c.X - c.W/2
			y := c.Y - c.H/2
			if svg.Paints(shape) {
				fills = append(fills, svg.Fill{Box: [4]float64{x, y, x + c.W, y + c.H}, Shape: shape})
			}
			fillOpacity := ` fill-opacity="0.5"`
			if svg.SetsFill(shape) {
				fillOpacity = ""
			}
			box := ` fill="var(--_a)"` + fillOpacity + ` stroke="var(--_b)"` + shape
			titleAttrs := ` class="pele-cluster-label"` + muted + textAttrs
			var body string
			if node.boundaryType == "package" {
				tabW := math.Min(view.label.Width+2*TAB_PAD+8, c.W)
				tabH := view.label.Height + 2*TAB_PAD
				body = `<rect class="pele-boundary-tab" x="` + svg.Num(x) + `" y="` + svg.Num(y) + `" width="` + svg.Num(tabW) + `" height="` + svg.Num(tabH) + `"` + box + `/>` +
					`<rect x="` + svg.Num(x) + `" y="` + svg.Num(y+tabH) + `" width="` + svg.Num(c.W) + `" height="` + svg.Num(c.H-tabH) + `"` + box + `/>` +
					svg.LabelSvg(view.label, x+tabW/2, y+tabH/2, titleAttrs, icons)
			} else {
				body = `<rect x="` + svg.Num(x) + `" y="` + svg.Num(y) + `" width="` + svg.Num(c.W) + `" height="` + svg.Num(c.H) + `"` + svg.Rounded(box) + `/>` +
					svg.LabelSvg(view.label, titleX(c, view.label.Width, crossings), y+8+view.label.Height/2, titleAttrs, icons)
			}
			svg.WriteStrings(&clusters, `<g class="pele-cluster pele-boundary`, classes, `" data-id="`, id, `">`, body, `</g>`)
			continue
		}

		var body, kind string
		switch view.kind {
		case kActor:
			kind = "pele-actor"
			top := -h / 2
			stereotypeY := top + FIGURE_H + FIGURE_GAP + view.stereotype.Height/2
			body = `<g class="pele-actor-figure" transform="translate(0,` + svg.Num(top+FIGURE_H/2) + `)">` + figure(view, icons) + `</g>` +
				svg.LabelSvg(view.stereotype, 0, stereotypeY, stereotypeAttrs, icons) +
				svg.LabelSvg(view.label, 0, h/2-view.label.Height/2, ` class="pele-label"`+textAttrs, icons)
		case kJson:
			kind = "pele-json"
			x0 := -w / 2
			y := -h/2 + view.headH
			var cells strings.Builder
			lines := `M` + svg.Num(x0) + `,` + svg.Num(y) + `H` + svg.Num(x0+w)
			if view.keyW > 0 {
				lines += `M` + svg.Num(x0+view.keyW) + `,` + svg.Num(y) + `V` + svg.Num(h/2)
			}
			for _, row := range view.rows {
				key, value := row[0], row[1]
				rowH := math.Max(math.Max(key.Height, value.Height), js.Round(small*1.5)) + ROW_PAD
				cells.WriteString(svg.LabelSvg(key, x0+CELL_PAD+key.Width/2, y+rowH/2, ` class="pele-json-key"`+muted, icons))
				cells.WriteString(leftLabel(value, x0+view.keyW+CELL_PAD, y+rowH/2, ` class="pele-json-value"`+color+textAttrs, icons))
				y += rowH
			}
			body = `<rect x="` + svg.Num(x0) + `" y="` + svg.Num(-h/2) + `" width="` + svg.Num(w) + `" height="` + svg.Num(h) + `"` + svg.Rounded(SHAPE_ATTRS+shape) + `/>` +
				`<path class="pele-json-lines" d="` + lines + `" fill="none" stroke="var(--_b)"` + view.style.Line + `/>` +
				svg.LabelSvg(view.label, 0, -h/2+view.headH/2, ` class="pele-label" font-weight="var(--_hw)"`+color+textAttrs, icons) +
				`<g font-size="` + js.Num(small) + `">` + cells.String() + `</g>`
		default:
			labelY := view.stereotype.Height / 2
			var outline string
			switch {
			case view.kind == kEllipse:
				kind = "pele-usecase"
				outline = `<ellipse rx="` + svg.Num(w/2) + `" ry="` + svg.Num(h/2) + `"` + SHAPE_ATTRS + shape + `/>`
				if node.business {
					// The slash of a business use case, cutting off the right end of the oval beside the text.
					from := w/2 - 15
					to := w/2 - 5
					arc := func(at float64) float64 {
						q := at / (w / 2)
						return (h / 2) * math.Sqrt(1-float64(q*q))
					}
					outline += `<path class="pele-business" d="M` + svg.Num(from) + `,` + svg.Num(arc(from)) + `L` + svg.Num(to) + `,` + svg.Num(-arc(to)) + `" fill="none" stroke="var(--_b)"` + view.style.Line + `/>`
				}
			case node.shape == "note":
				kind = "pele-note"
				r := w / 2
				b := h / 2
				outline = `<path d="M` + svg.Num(-r) + `,` + svg.Num(-b) + `H` + svg.Num(r-FOLD) + `L` + svg.Num(r) + `,` + svg.Num(FOLD-b) + `V` + svg.Num(b) + `H` + svg.Num(-r) +
					`Z" fill="var(--_a)" stroke="var(--_b)" stroke-linejoin="round"` + shape + `/>` +
					`<path d="M` + svg.Num(r-FOLD) + `,` + svg.Num(-b) + `V` + svg.Num(FOLD-b) + `H` + svg.Num(r) + `" fill="none" stroke="var(--_b)" stroke-linejoin="round"` + view.style.Line + `/>`
			default:
				kind = "pele-usecase"
				outline = `<rect x="` + svg.Num(-w/2) + `" y="` + svg.Num(-h/2) + `" width="` + svg.Num(w) + `" height="` + svg.Num(h) + `"` + svg.Rounded(SHAPE_ATTRS+shape) + `/>`
			}

			noteSize := ""
			if node.shape == "note" {
				noteSize = ` font-size="` + js.Num(small) + `"`
			}
			body = outline +
				svg.LabelSvg(view.stereotype, 0, -view.label.Height/2, ` class="pele-stereotype" font-size="`+js.Num(tiny)+`"`+muted, icons) +
				svg.LabelSvg(view.label, 0, labelY, ` class="pele-label"`+noteSize+color+textAttrs, icons)
		}
		svg.WriteStrings(&nodesOut, `<g class="pele-node `, kind, classes, `" data-id="`, id, `" transform="translate(`, svg.Num(c.X), `,`, svg.Num(c.Y), `)">`, body, `</g>`)
	}

	var geo *types.Geometry
	if options.Geometry != nil {
		geo = geometry(views, cnodes, width, height)
	}

	var casings, edgesOut, labelsOut strings.Builder
	loopSeen := map[int]int{}
	for i, ce := range cedges {
		edge := drawn[i]
		label := edgeLabels[i]
		var style *svg.ResolvedStyle
		if len(edge.style) > 0 || len(edge.cssCompiledStyles) > 0 {
			s := svg.ResolveStyle(append(append([]string{}, edge.cssCompiledStyles...), edge.style...))
			style = &s
		}
		startType := edge.arrowTypeStart
		endType := edge.arrowTypeEnd
		var path svg.EdgePath
		if ce.Src == ce.Dst {
			c := cnodes[ce.Src]
			view := &views[ce.Src]
			k := loopSeen[ce.Src]
			loopSeen[ce.Src] = k + 1
			side := 1
			if sideways {
				side = 2
			}
			spread := math.Min(sideLength(view, side)/2, 8+float64(float64(k)*6))
			reach := LOOP + float64(float64(k)*8)
			ax, ay := anchor(view, c, side, -spread)
			bx, by := anchor(view, c, side, spread)
			length := js.Or(js.Hypot(reach, spread), 1)
			ux, uy := reach/length, spread/length
			if sideways {
				ux, uy = spread/length, reach/length
			}
			st := trim(startType)
			et := trim(endType)
			// Both ends leave the node leaning away from each other, and the curve joins them outside it.
			out := func(x, y, sign, far float64) string {
				f := 0.0
				if far != 0 {
					f = 1
				}
				if sideways {
					return svg.Num(x+float64(float64(sign*spread)*f)) + `,` + svg.Num(y+far)
				}
				return svg.Num(x+far) + `,` + svg.Num(y+float64(float64(sign*spread)*f))
			}
			sx, sy := ax+float64(ux*st), ay-float64(uy*st)
			if sideways {
				sx, sy = ax-float64(ux*st), ay+float64(uy*st)
			}
			ex := bx + float64(ux*et)
			ey := by + float64(uy*et)
			path = svg.EdgePath{
				D:  `M` + svg.Num(sx) + `,` + svg.Num(sy) + `C` + out(ax, ay, -1, reach) + ` ` + out(bx, by, 1, reach) + ` ` + svg.Num(ex) + `,` + svg.Num(ey),
				Sx: ax, Sy: ay, Ex: bx, Ey: by, Edx: -ux, Edy: -uy,
			}
			if sideways {
				path.Sdx, path.Sdy = ux, -uy
				ce.LabelX = c.X
				ce.LabelY = math.Max(ay, by) + float64(reach*0.75) + 6 + label.Height/2
			} else {
				path.Sdx, path.Sdy = -ux, uy
				ce.LabelX = math.Max(ax, bx) + float64(reach*0.75) + 8 + label.Width/2
				ce.LabelY = c.Y
			}
		} else {
			if len(ce.Route) < 6 {
				continue
			}
			st, et := trim(startType), trim(endType)
			path = svg.RoutePath(ce.Route, "", st, et, svg.DefaultReach(st), svg.DefaultReach(et))
		}
		line := ""
		if style != nil {
			line = style.Line
		}
		under, onFills := svg.FillUnder(path.D, []float64{path.Sx, path.Sy, path.Ex, path.Ey}, fills, path.Tokens)
		over, overOk := "", onFills
		if under != nil {
			over = svg.EdgeInk(under, line)
		}
		color := over
		if color == "" {
			color = "var(--_l)"
			if style != nil && style.Stroke != "" {
				color = svg.Esc(style.Stroke)
			}
		}
		if overOk && over == "" {
			bg := "var(--_bg)"
			casings.WriteString(svg.Casing(path.D, mark(startType, path.Sx, path.Sy, path.Sdx, path.Sdy, bg, bg)+mark(endType, path.Ex, path.Ey, path.Edx, path.Edy, bg, bg), line, false))
		}
		// Mermaid animates these dashes. A still picture keeps the dashes.
		dashes := ""
		switch {
		case edge.internal:
			dashes = "2 3"
		case edge.pattern == "dotted":
			dashes = "5 4"
		case edge.animate:
			dashes = "9 5"
		}
		marks := mark(startType, path.Sx, path.Sy, path.Sdx, path.Sdy, color, "") + mark(endType, path.Ex, path.Ey, path.Edx, path.Edy, color, "")
		if under != nil && over != "" {
			marks = svg.HollowOn(marks, under)
		}
		stroke := ""
		if over != "" {
			stroke = ` stroke="` + over + `"`
		}
		svg.WriteStrings(&edgesOut, `<g class="pele-edge pele-`, edge.relationshipType, svg.ClassNames(trimRelationship(edge.classes)), `" data-id="`, svg.EscText(edge.id), `"`, stroke, line, `>`,
			`<path d="`, path.D, `"`)
		if dashes != "" {
			svg.WriteStrings(&edgesOut, ` stroke-dasharray="`, dashes, `"`)
		}
		svg.WriteStrings(&edgesOut, `/>`, marks, `</g>`)
		semantic := edge.relationshipType == "include" || edge.relationshipType == "extend"
		if label.Width > 0 {
			attrs := ""
			if semantic {
				attrs = ` fill="var(--_m)"`
			}
			if style != nil {
				attrs += style.Text
			}
			labelsOut.WriteString(svg.EdgeLabelSvg(edge.id, label, ce.LabelX, ce.LabelY, attrs, icons))
		}
		if geo != nil {
			stroke := "normal"
			if dashes != "" {
				stroke = "dotted"
			}
			var route []float64
			if ce.Src != ce.Dst {
				route = append([]float64{}, ce.Route...)
				actorEnd(route, 0, &views[ce.Src], cnodes[ce.Src], geo.Nodes[ce.Src].W)
				actorEnd(route, len(route)-3, &views[ce.Dst], cnodes[ce.Dst], geo.Nodes[ce.Dst].W)
			}
			geo.Edges = append(geo.Edges, types.GeoEdge{From: ce.Src, To: ce.Dst, Route: route, Start: textMarker(startType), End: textMarker(endType),
				Stroke: stroke, Label: label.Plain(), LabelX: ce.LabelX, LabelY: ce.LabelY})
		}
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
		parts = append(parts, `<g class="pele-edge-labels" font-size="`, js.Num(small), `">`, labelsOut.String(), `</g>`)
	}
	parts = append(parts, `<g class="pele-nodes">`, nodesOut.String(), `</g>`)
	out := svg.SvgDocument("usecase", width, height, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr}, parts...)

	return types.Rendered{SVG: out, Width: width, Height: height, Links: []types.LinkInfo{}, Geometry: geo}
}

// titleX: a boundary's title sits in the middle of its top edge. Where edges enter through that edge it
// moves into the stretch between them that is nearest the middle, or the widest if none has room.
func titleX(c *layout.CNode, width float64, crossings svg.Crossings) float64 {
	xs, ok := crossings[js.Round(c.Y-c.H/2)]
	if width == 0 || !ok {
		return c.X
	}
	left := c.X - c.W/2 + 6
	right := c.X + c.W/2 - 6
	var cuts []float64
	for _, x := range xs {
		if x > left && x < right {
			cuts = append(cuts, x)
		}
	}
	js.Sort(cuts, func(a, b float64) float64 { return a - b })
	cuts = append(cuts, right)
	need := width/2 + 6
	best := c.X
	bestOff := math.Inf(1)
	widest := 0.0
	from := left
	for _, cut := range cuts {
		if cut-from >= 2*need {
			x := math.Min(math.Max(c.X, from+need), cut-need)
			if math.Abs(x-c.X) < bestOff {
				bestOff = math.Abs(x - c.X)
				best = x
			}
		} else if math.IsInf(bestOff, 1) && cut-from > widest {
			widest = cut - from
			best = (from + cut) / 2
		}
		from = cut
	}
	return best
}

func leftLabel(label text.Label, x, cy float64, attrs string, icons text.IconResolver) string {
	if len(label.Lines) < 2 {
		return svg.LabelSvg(label, x+label.Width/2, cy, attrs, icons)
	}
	var out strings.Builder
	for i := range label.Lines {
		out.WriteString(svg.LabelSvg(text.LabelLine(label, i), x+label.Widths[i]/2, cy-label.Height/2+text.LineMiddle(label, i), attrs, icons))
	}
	return out.String()
}

// actorEnd moves the end of a route at an actor from its figure to the side of the box
// sketch/text draws around the actor and its name, which is w wide. Not in pele.
func actorEnd(route []float64, at int, view *View, c *layout.CNode, w float64) {
	if view.kind != kActor {
		return
	}
	dx := route[at] - c.X
	if math.Abs(math.Abs(dx)-(FIGURE_W/2+3)) < 1 {
		route[at] = c.X + math.Copysign(w/2, dx)
		return
	}
	if dy := route[at+1] - c.Y; math.Abs(dy) > view.h/2 {
		route[at+1] = c.Y + math.Copysign(view.h/2, dy)
	}
}

// textMarker is the marker sketch/text draws for an end: a generalization's hollow triangle
// is drawn as an arrow head.
func textMarker(typ string) string {
	if typ == "extension" {
		return "arrow_point"
	}
	return typ
}

// geometry gives the actors, use cases, notes and JSON tables as flowchart nodes and the
// boundaries as groups, so that sketch/text draws the diagram as it draws a flowchart.
// The edges are added as they are drawn. Not in pele.
func geometry(views []View, cnodes []*layout.CNode, width, height float64) *types.Geometry {
	g := &types.Geometry{Width: width, Height: height}
	for i := range views {
		view := &views[i]
		c := cnodes[i]
		n := types.GeoNode{ID: view.node.id, Shape: "rect", X: c.X, Y: c.Y, W: view.w, H: view.h, Parent: c.Parent}
		stereotype := view.stereotype.Plain()
		switch view.kind {
		case kBoundary:
			n.Group = true
			n.W, n.H = c.W, c.H
			n.Label = view.label.Plain()
		case kActor:
			// A stick figure in text, with no box. The room a box would take around the
			// name is added, so that the actor does not set a coarser scale than a box does.
			n.Shape = "actor"
			n.W += 32
			n.Label = append(append([]string{" o ", "/|\\", "/ \\"}, stereotype...), view.label.Plain()...)
		case kEllipse:
			n.Shape = "ellipse"
			n.Label = append(stereotype, view.label.Plain()...)
		case kJson:
			// A row reads "key: value", two characters longer than the table's columns.
			n.W += 16
			n.Label = view.label.Plain()
			for _, row := range view.rows {
				key := row[0].Plain()
				for k, value := range row[1].Plain() {
					if k == 0 && len(key) > 0 && key[0] != "" {
						n.Label = append(n.Label, key[0]+": "+value)
					} else {
						n.Label = append(n.Label, "  "+value)
					}
				}
			}
		default:
			n.Label = append(stereotype, view.label.Plain()...)
		}
		g.Nodes = append(g.Nodes, n)
	}
	return g
}
