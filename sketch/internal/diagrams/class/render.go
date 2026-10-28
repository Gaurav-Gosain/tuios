// Ported from pele src/diagrams/class/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package class

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
	NODE_SEP      = 40
	EDGE_SEP      = 16
	RANK_SEP      = 56
	GROUP_PAD     = 20
	LOOP          = 26
	PAD_X         = 12
	TITLE_PAD     = 8
	SECTION_PAD   = 6
	EMPTY_SECTION = 10
	MIN_WIDTH     = 64
	FOLD          = 8
	LINE          = "var(--_l)"
)

func isTarget(target string) bool {
	return target == "_self" || target == "_blank" || target == "_parent" || target == "_top"
}

func numberOption(config types.Config, key string, fallback float64) float64 {
	if value, ok := config[key].(float64); ok && value > 0 {
		return math.Min(value, 2000)
	}
	return fallback
}

type Line struct {
	text       string
	width      float64
	classifier string
}

type View struct {
	node        *GraphNode
	style       svg.ResolvedStyle
	label       text.Label
	w           float64
	h           float64
	loops       int
	annotations []annotation
	attributes  []Line
	methods     []Line
	titleH      float64
	attributesH float64
	fontSize    float64
}

// annotation is pele's `string | Label`: the text, and the label when it holds typeset math.
type annotation struct {
	text  string
	label *text.Label
}

type End struct {
	// The edge whose route this end is on.
	edge   int
	at     int
	toward float64
	order  int
	room   float64
	side   int
	// Whether the text of this end goes on the right of, or below, its line.
	after bool
}

func memberLine(member *ClassMember, measurer text.TextMeasurer, size float64, mono bool) Line {
	display, _ := member.GetDisplayDetails()
	txt := js.Trim(text.DecodeEntities(display))
	style := 0
	if member.Classifier == "*" {
		style = text.Italic
	}
	if mono {
		style |= text.Mono
	}
	return Line{text: txt, width: measurer.Width(txt, size, style), classifier: member.Classifier}
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

// keptStyles is a Map from property to declaration that keeps insertion order.
type keptStyles struct {
	keys []string
	vals map[string]string
}

func (k *keptStyles) set(prop, decl string) {
	if _, ok := k.vals[prop]; !ok {
		k.keys = append(k.keys, prop)
	}
	k.vals[prop] = decl
}

func (k *keptStyles) clear() {
	k.keys = k.keys[:0]
	clear(k.vals)
}

func renderClass(db *ClassDb, config types.Config, options types.RenderOptions) types.Rendered {
	fitted := func(down bool) types.Rendered {
		return common.Tighten(options, func(tight bool) types.Rendered { return draw(db, config, options, down, tight) })
	}
	if common.RunsAcross(db.Direction) {
		return common.TurnToFit(options, fitted)
	}
	return fitted(false)
}

// draw: `turned` draws a diagram that runs across as one that runs down. `tight` gives up the room
// that keeps a namespace's name clear of an edge coming in beside it.
func draw(db *ClassDb, config types.Config, options types.RenderOptions, turned bool, tight bool) types.Rendered {
	conf, _ := config["class"].(map[string]any)
	hierarchical := true
	if v, ok := conf["hierarchicalNamespaces"].(bool); ok && !v {
		hierarchical = false
	}
	graph := buildClassGraph(db, hierarchical)
	monospaced := options.HasMonospace(types.MonospaceMembers)
	// Explicit class fonts override monospace.
	mono := func(style svg.ResolvedStyle) bool { return monospaced && !svg.Declares(style.Text, "font-family") }
	hideEmpty := false
	if v, ok := conf["hideEmptyMembersBox"].(bool); ok && v {
		hideEmpty = true
	}
	size := types.Or(options.FontSize, 16)
	small := js.Round(size * 0.875)
	tiny := js.Round(size * 0.8)
	tinyHeight := js.Round(tiny * 1.4)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)
	// Keep annotations and cardinalities literal unless math is typeset.
	mathText := func(raw string, fontSize float64) *text.Label {
		if !strings.Contains(raw, "$$") {
			return nil
		}
		label := text.LayoutLabel(raw, false, measurer, fontSize, 4000, 0)
		for _, line := range label.Lines {
			if text.HasMath(line) {
				return &label
			}
		}
		return nil
	}
	cardWidth := func(raw string) float64 {
		if raw == "" {
			return 0
		}
		if m := mathText(raw, tiny); m != nil {
			return m.Width
		}
		return measurer.Width(text.DecodeEntities(raw), tiny, 0)
	}
	rootDir := layout.Direction(db.Direction)
	if turned {
		rootDir = "TB"
	}
	icons := options.Icons
	num := svg.Num

	// A class may carry the declarations of many classDefs many times over. Each distinct declaration
	// is checked once, and only the last value of each property that has an effect is resolved.
	effect := map[string]string{}
	type pair struct{ prop, decl string }
	fromDefs := map[string][]pair{}
	kept := &keptStyles{vals: map[string]string{}}
	keep := func(decl string) {
		prop, ok := effect[decl]
		if !ok {
			one := svg.ResolveStyle([]string{decl})
			if one.Shape == "" && one.Text == "" {
				prop = ""
			} else {
				end := strings.IndexByte(decl, ':')
				if end == -1 {
					// slice(0, -1) drops the last character.
					_, size := utf8.DecodeLastRuneInString(decl)
					end = len(decl) - size
				}
				prop = strings.ToLower(js.Trim(decl[:end]))
			}
			effect[decl] = prop
		}
		if prop != "" {
			kept.set(prop, decl)
		}
	}
	classStyle := func(cls *ClassNode) svg.ResolvedStyle {
		kept.clear()
		base, ok := fromDefs[cls.CssClasses]
		if !ok {
			for _, name := range strings.Split(cls.CssClasses, " ") {
				if def, ok := db.StyleClasses.Get(name); ok {
					for _, s := range def.Styles {
						for _, part := range strings.Split(s, ",") {
							keep(part)
						}
					}
				}
			}
			base = make([]pair, len(kept.keys))
			for k, prop := range kept.keys {
				base[k] = pair{prop, kept.vals[prop]}
			}
			fromDefs[cls.CssClasses] = base
		} else {
			for _, p := range base {
				kept.set(p.prop, p.decl)
			}
		}
		for _, s := range cls.Styles {
			keep(s)
		}
		if len(kept.keys) == 0 {
			return svg.ResolveStyle(nil)
		}
		values := make([]string, len(kept.keys))
		for k, prop := range kept.keys {
			values[k] = kept.vals[prop]
		}
		return svg.ResolveStyle(values)
	}

	children := make([]int32, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if node.Parent >= 0 {
			children[node.Parent]++
		}
	}

	// Namespaces that an edge may cross into are made wide enough for the title to sit beside it.
	crossed := make([]uint8, len(graph.Nodes))
	for _, edge := range graph.Edges {
		if edge.Source < 0 || edge.Target < 0 {
			continue
		}
		a := graph.Nodes[edge.Source].Parent
		b := graph.Nodes[edge.Target].Parent
		if a == b {
			continue
		}
		for g := a; g >= 0 && crossed[g] == 0; g = graph.Nodes[g].Parent {
			crossed[g] = 1
		}
		for g := b; g >= 0 && crossed[g] == 0; g = graph.Nodes[g].Parent {
			crossed[g] = 1
		}
	}

	base := func(node *GraphNode, label text.Label, w, h, fontSize float64) *View {
		return &View{
			node:     node,
			style:    svg.ResolveStyle(nil),
			label:    label,
			w:        w,
			h:        h,
			fontSize: fontSize,
		}
	}

	classView := func(node *GraphNode, cls *ClassNode) *View {
		style := classStyle(cls)
		fontSize := size
		if style.FontSize != 0 {
			fontSize = style.FontSize
		}
		memberSize := js.Round(fontSize * 0.875)
		lineHeight := js.Round(memberSize * 1.5)
		// The generic type is escaped so that `<T>` is not read as a tag.
		generic := ""
		if cls.Type != "" {
			g := common.ParseGenericTypes(cls.Type)
			g = strings.ReplaceAll(g, "<", "&lt;")
			g = strings.ReplaceAll(g, ">", "&gt;")
			generic = "&lt;" + g + "&gt;"
		}
		labelStyle := text.Bold
		if style.Italic {
			labelStyle |= text.Italic
		}
		label := text.LayoutLabel(cls.Label+generic, false, measurer, fontSize, 4000, labelStyle)
		var annotations []annotation
		for _, a := range cls.Annotations {
			annotations = append(annotations, annotation{"«" + js.Trim(text.DecodeEntities(a)) + "»", mathText("«"+js.Trim(a)+"»", tiny)})
		}
		var attributes, methods []Line
		for _, m := range cls.Members {
			attributes = append(attributes, memberLine(m, measurer, memberSize, mono(style)))
		}
		for _, m := range cls.Methods {
			methods = append(methods, memberLine(m, measurer, memberSize, mono(style)))
		}
		inner := label.Width
		annotationsH := 0.0
		for _, a := range annotations {
			if a.label == nil {
				inner = math.Max(inner, measurer.Width(a.text, tiny, 0))
				annotationsH += tinyHeight
			} else {
				inner = math.Max(inner, a.label.Width)
				annotationsH += math.Max(tinyHeight, a.label.Height)
			}
		}
		for _, line := range attributes {
			inner = math.Max(inner, line.width)
		}
		for _, line := range methods {
			inner = math.Max(inner, line.width)
		}
		sections := !(hideEmpty && len(attributes) == 0 && len(methods) == 0)
		section := func(count int) float64 {
			if !sections {
				return 0
			}
			if count > 0 {
				return 2*SECTION_PAD + float64(float64(count)*lineHeight)
			}
			return EMPTY_SECTION
		}
		titleH := 2*TITLE_PAD + annotationsH + math.Max(label.Height, js.Round(fontSize*1.5))
		attributesH := section(len(attributes))
		return &View{
			node:        node,
			style:       style,
			label:       label,
			w:           math.Max(inner+2*PAD_X, MIN_WIDTH),
			h:           titleH + attributesH + section(len(methods)),
			annotations: annotations,
			attributes:  attributes,
			methods:     methods,
			titleH:      titleH,
			attributesH: attributesH,
			fontSize:    fontSize,
		}
	}

	views := make([]*View, 0, len(graph.Nodes))
	cnodes := make([]*layout.CNode, 0, len(graph.Nodes))
	for i, node := range graph.Nodes {
		var view *View
		var c *layout.CNode
		if node.IsGroup {
			label := text.LayoutLabel(node.Label, false, measurer, size, 4000, 0)
			view = base(node, label, 0, 0, size)
			if children[i] > 0 {
				c = layout.Cnode(0, 0, -1)
				c.IsGroup = true
				c.PadX = GROUP_PAD
				if label.Height > 0 {
					c.PadTop = label.Height + 16
				} else {
					c.PadTop = GROUP_PAD
				}
				c.PadBottom = GROUP_PAD
				c.MinW = label.Width + 2*GROUP_PAD - 12
				if crossed[i] == 1 && !tight {
					c.MinW += math.Min(label.Width+24, 240)
				}
			} else {
				view.w = math.Max(label.Width+2*GROUP_PAD, 80)
				view.h = label.Height + 2*GROUP_PAD
				c = layout.Cnode(view.w, view.h, -1)
			}
		} else if node.ClassNode != nil {
			view = classView(node, node.ClassNode)
			c = layout.Cnode(view.w, view.h, -1)
		} else if node.Shape == "note" {
			label := text.LayoutLabel(node.Label, true, measurer, small, 260, 0)
			view = base(node, label, math.Max(label.Width+2*PAD_X, 40), label.Height+2*TITLE_PAD, small)
			c = layout.Cnode(view.w, view.h, -1)
		} else {
			label := text.LayoutLabel(node.Label, false, measurer, small, 200, 0)
			view = base(node, label, math.Max(label.Width, 12)+4, math.Max(label.Height, 12), small)
			c = layout.Cnode(view.w, view.h, -1)
		}
		c.Parent = node.Parent
		c.Seq = i
		views = append(views, view)
		cnodes = append(cnodes, c)
	}

	var cedges []*layout.CEdge
	var drawn []*GraphEdge
	var edgeLabels []text.Label
	var startRoom []float64
	var endRoom []float64
	// Edges leave one side of a class and arrive at the other, so a class with many of them
	// is widened until they, and the text at their ends, fit side by side.
	crowd := make([]float64, len(views)*4)
	meet := func(item, slot int, room float64) {
		crowd[item*4+slot]++
		if room > crowd[item*4+slot+1] {
			crowd[item*4+slot+1] = room
		}
	}
	for _, edge := range graph.Edges {
		if edge.Source < 0 || edge.Target < 0 {
			continue
		}
		label := text.LayoutLabel(edge.Label, true, measurer, small, 200, 0)
		startW := cardWidth(edge.StartLabelRight)
		endW := cardWidth(edge.EndLabelLeft)
		labelW := 0.0
		if label.Width > 0 {
			labelW = label.Width + 12
		}
		if edge.Source == edge.Target {
			views[edge.Source].loops++
			cnodes[edge.Source].W += float64(2 * (LOOP + labelW))
		} else {
			meet(edge.Source, 0, startW)
			meet(edge.Target, 2, endW)
		}
		startRoom = append(startRoom, startW)
		endRoom = append(endRoom, endW)
		drawn = append(drawn, edge)
		edgeLabels = append(edgeLabels, label)
		labelH := 0.0
		if label.Height > 0 {
			labelH = label.Height + 4
		}
		cedges = append(cedges, &layout.CEdge{Src: edge.Source, Dst: edge.Target, Minlen: 1, LabelW: labelW, LabelH: labelH, Route: []float64{}})
	}

	if rootDir == "TB" || rootDir == "BT" {
		for i := 0; i < len(views); i++ {
			if views[i].node.ClassNode == nil {
				continue
			}
			need := 0.0
			for slot := 0; slot < 4; slot += 2 {
				count := crowd[i*4+slot]
				room := crowd[i*4+slot+1]
				if count > 1 {
					per := 14.0
					if room > 0 {
						per = room + 24
					}
					need = math.Max(need, float64((count-1)*per)+24)
				}
			}
			need = math.Min(need, 480)
			if need > views[i].w {
				cnodes[i].W += need - views[i].w
				views[i].w = need
			}
		}
	}

	lettered := false
	for i := 0; i < len(drawn) && !lettered; i++ {
		lettered = startRoom[i] > 0 || endRoom[i] > 0
	}
	rankSep := float64(RANK_SEP)
	if lettered {
		rankSep = RANK_SEP + 16
	}
	// Cardinalities at both ends of a vertical edge need room for their height between the ranks.
	tallCards := 0.0
	if rootDir == "TB" || rootDir == "BT" {
		cardHeight := func(raw string) float64 {
			if raw == "" {
				return 0
			}
			h := 0.0
			if m := mathText(raw, tiny); m != nil {
				h = m.Height
			}
			return math.Max(tinyHeight, h)
		}
		for _, edge := range drawn {
			tallCards = math.Max(tallCards, cardHeight(edge.StartLabelRight)+cardHeight(edge.EndLabelLeft)-float64(2*tinyHeight))
		}
	}
	lay := layout.CompoundLayout(cnodes, cedges, rootDir, layout.LayeredOptions{
		NodeSep: numberOption(conf, "nodeSpacing", NODE_SEP),
		EdgeSep: EDGE_SEP,
		RankSep: numberOption(conf, "rankSpacing", rankSep) + tallCards,
		PortSep: 20,
		Apart:   true,
	})

	// Every edge reaches the middle of a side. Where several share a side they are spread along it,
	// in the order of where they come from. An end with text runs straight for the length of the text,
	// which sits beside it on the side the line then bends toward, away from the neighbors that bend
	// the other way. Neighbors are set apart far enough for the text between them.
	sides := map[int][]*End{}
	var sideKeys []int
	type cardItem struct {
		text   string
		math   *text.Label
		x, y   float64
		w, h   float64
		parent int
	}
	var cards []cardItem
	attach := func(i int, at int, item int, room float64, order int) *End {
		route := cedges[i].Route
		c := cnodes[item]
		side := endSide(route, at, c)
		if side < 0 {
			return nil
		}
		slack := (c.W - views[item].w) / 2
		if side == 1 {
			route[at] -= slack
		} else if side == 3 {
			route[at] += slack
		}
		across := side & 1
		from := at - 3
		if at == 0 {
			from = 3
		}
		toward := route[from+across]
		bend := toward - route[at+across]
		after := bend >= 0
		if across == 1 {
			after = bend > 0
		}
		end := &End{edge: i, at: at, toward: toward, order: order, room: room, side: side, after: after}
		key := item*4 + side
		if _, ok := sides[key]; !ok {
			sideKeys = append(sideKeys, key)
		}
		sides[key] = append(sides[key], end)
		return end
	}
	starts := make([]*End, len(cedges))
	ends := make([]*End, len(cedges))
	for i, ce := range cedges {
		routed := ce.Src != ce.Dst && len(ce.Route) >= 6
		if routed {
			starts[i] = attach(i, 0, ce.Src, startRoom[i], i)
			ends[i] = attach(i, len(ce.Route)-3, ce.Dst, endRoom[i], i)
		}
	}
	// Whether a point lies on the border of one of the namespaces around a class.
	onBorder := func(item int, x, y float64) bool {
		for g, hops := cnodes[item].Parent, 0; g >= 0 && hops < 10000; g, hops = cnodes[g].Parent, hops+1 {
			c := cnodes[g]
			dx := math.Abs(x - c.X)
			dy := math.Abs(y - c.Y)
			if (math.Abs(dy-c.H/2) < 0.5 && dx <= c.W/2+0.5) || (math.Abs(dx-c.W/2) < 0.5 && dy <= c.H/2+0.5) {
				return true
			}
		}
		return false
	}
	var gaps []float64
	for _, key := range sideKeys {
		list := sides[key]
		if len(list) < 2 {
			continue
		}
		item := key >> 2
		across := key & 1
		length := views[item].w
		if across == 1 {
			length = views[item].h
		}
		slices.SortStableFunc(list, func(a, b *End) int {
			d := a.toward - b.toward
			if d < 0 {
				return -1
			}
			if d > 0 {
				return 1
			}
			return a.order - b.order
		})
		total := 0.0
		for j := 0; j+1 < len(list); j++ {
			a := list[j]
			b := list[j+1]
			gap := 24.0
			if across == 1 {
				gap = 20
			}
			if a.room > 0 && a.after {
				if across == 1 {
					gap += tinyHeight + 6
				} else {
					gap += a.room
				}
			}
			if b.room > 0 && !b.after {
				if across == 1 {
					gap += tinyHeight + 6
				} else {
					gap += b.room
				}
			}
			for len(gaps) <= j {
				gaps = append(gaps, 0)
			}
			gaps[j] = gap
			total += gap
		}
		scale := math.Min(1, (length-24)/total)
		if scale <= 0 {
			continue
		}
		center := cnodes[item].X
		if across == 1 {
			center = cnodes[item].Y
		}
		at := center - (total*scale)/2
		for j := 0; j < len(list); j++ {
			route := cedges[list[j].edge].Route
			end := list[j].at
			// An edge that crosses the border of the class's namespace close by runs straight to it:
			// the point where it crosses moves into line with its end.
			step := -3
			if end == 0 {
				step = 3
			}
			first := end + step
			border := -1
			for k := first; k >= 0 && k < len(route) && math.Abs(route[k+across]-route[first+across]) < 0.5; k += step {
				if onBorder(item, route[k], route[k+1]) {
					border = k
					break
				}
			}
			if border >= 0 && math.Abs(route[first+across]-at) <= 24 {
				for k := first; k != border+step; k += step {
					route[k+across] = at
				}
			}
			route[end+across] = at
			if j < len(gaps) {
				at += float64(gaps[j] * scale)
			}
		}
	}

	straighten := func(ce *layout.CEdge, end *End, room float64, first bool) {
		if end == nil || room <= 0 {
			return
		}
		route := ce.Route
		across := end.side & 1
		at := len(route) - 3
		if first {
			at = 0
		}
		along := at + 1 - across
		from := at - 3
		if first {
			from = 3
		}
		reach := math.Abs(route[from+1-across] - route[along])
		length := tinyHeight + 6
		if across == 1 {
			length = room + 10
		}
		length = math.Min(length, reach*0.35)
		if length < 4 {
			return
		}
		point := []float64{route[at], route[at+1], route[at+2]}
		if end.side == 0 || end.side == 3 {
			point[1-across] -= length
		} else {
			point[1-across] += length
		}
		insert := at
		if first {
			insert = 3
		}
		ce.Route = slices.Insert(route, insert, point...)
	}
	for i, ce := range cedges {
		straighten(ce, starts[i], startRoom[i], true)
		straighten(ce, ends[i], endRoom[i], false)
	}

	minX := 0.0
	minY := 0.0
	maxX := lay.Width
	maxY := lay.Height
	card := func(parent int, raw string, width, x, y float64, side int, after bool) {
		if raw == "" {
			return
		}
		m := mathText(raw, tiny)
		h := tinyHeight
		if m != nil {
			h = math.Max(tinyHeight, m.Height)
		}
		cx := x
		cy := y
		if side&1 != 0 {
			if side == 3 {
				cx -= 5 + width/2
			} else {
				cx += 5 + width/2
			}
			if after {
				cy += 7 + h/2
			} else {
				cy -= 7 + h/2
			}
		} else {
			if after {
				cx += 9 + width/2
			} else {
				cx -= 9 + width/2
			}
			if side == 0 {
				cy -= 3 + h/2
			} else {
				cy += 3 + h/2
			}
		}
		cards = append(cards, cardItem{text: text.DecodeEntities(raw), math: m, x: cx, y: cy, w: width, h: h, parent: parent})
		minX = math.Min(minX, cx-width/2)
		maxX = math.Max(maxX, cx+width/2)
		minY = math.Min(minY, cy-h/2)
		maxY = math.Max(maxY, cy+h/2)
	}
	loopSeen := map[int]int{}
	loopAt := make([]int, len(cedges))
	for i, ce := range cedges {
		edge := drawn[i]
		route := ce.Route
		if ce.Src == ce.Dst {
			k := loopSeen[ce.Src]
			loopSeen[ce.Src] = k + 1
			loopAt[i] = k
			c := cnodes[ce.Src]
			view := views[ce.Src]
			x := c.X + view.w/2
			spread := math.Min(view.h/2-4, 8+float64(float64(k)*6))
			card(ce.Src, edge.StartLabelRight, startRoom[i], x, c.Y-spread, 1, false)
			card(ce.Dst, edge.EndLabelLeft, endRoom[i], x, c.Y+spread, 1, true)
			ce.LabelX = c.X + view.w/2 + LOOP + float64(k*8) + 4 + ce.LabelW/2
			ce.LabelY = c.Y
		} else if len(route) >= 6 {
			last := len(route) - 3
			from := starts[i]
			to := ends[i]
			fromSide, fromAfter := 2, true
			if from != nil {
				fromSide, fromAfter = from.side, from.after
			}
			toSide, toAfter := 0, true
			if to != nil {
				toSide, toAfter = to.side, to.after
			}
			card(ce.Src, edge.StartLabelRight, startRoom[i], route[0], route[1], fromSide, fromAfter)
			card(ce.Dst, edge.EndLabelLeft, endRoom[i], route[last], route[last+1], toSide, toAfter)
		}
	}

	if options.MaxWidth != nil && *options.MaxWidth > 0 && maxX-minX+float64(2*pad) > *options.MaxWidth {
		available := *options.MaxWidth
		cardNodes := make([]*layout.CNode, len(cards))
		for i, item := range cards {
			cardNodes[i] = layout.Cnode(item.w, item.h, item.parent)
			cardNodes[i].X = item.x
			cardNodes[i].Y = item.y
		}
		all := append(append(make([]*layout.CNode, 0, len(cnodes)+len(cardNodes)), cnodes...), cardNodes...)
		if wrapped, ok := layout.WrapComponents(all, cedges, math.Max(1, available-float64(2*pad)), numberOption(conf, "nodeSpacing", NODE_SEP)); ok {
			for i := range cards {
				cards[i].x = cardNodes[i].X
				cards[i].y = cardNodes[i].Y
			}
			minX, minY = 0, 0
			maxX = wrapped.Width
			maxY = wrapped.Height
		}
	}

	ox := pad - minX
	oy := pad - minY
	width := math.Ceil(maxX - minX + float64(2*pad))
	height := math.Ceil(maxY - minY + float64(2*pad))
	var links []types.LinkInfo

	crossings := svg.Crossings{}
	if len(graph.Nodes) > 0 && graph.Nodes[0].IsGroup {
		for _, e := range cedges {
			svg.MarkCrossings(crossings, e.Route)
		}
	}
	parents := make([]int, len(graph.Nodes))
	for i, node := range graph.Nodes {
		parents[i] = node.Parent
	}

	classSvg := func(view *View, cls *ClassNode, id string, at string) string {
		w, h, style := view.w, view.h, view.style
		left := -w / 2
		top := -h / 2
		memberSize := js.Round(view.fontSize * 0.875)
		lineHeight := js.Round(memberSize * 1.5)
		var body strings.Builder
		body.WriteString(`<rect x="` + num(left) + `" y="` + num(top) + `" width="` + num(w) + `" height="` + num(h) + `" fill="var(--_s)" stroke="var(--_b)"` + svg.Rounded(style.Shape) + `/>`)
		ink := svg.InkFill(style, "")
		muted := ink
		if muted == "" {
			muted = ` fill="var(--_m)"`
		}
		first := top + view.titleH
		second := first + view.attributesH
		if h > view.titleH {
			stroke := ""
			if style.Stroke != "" {
				stroke = ` style="stroke:` + svg.Esc(style.Stroke) + `"`
			}
			body.WriteString(`<path class="pele-divider" d="M` + num(left) + `,` + num(first) + `H` + num(-left) + `M` + num(left) + `,` + num(second) + `H` + num(-left) + `" fill="none" stroke="var(--_b)"` + stroke + `/>`)
		}
		y := top + TITLE_PAD
		for _, annotation := range view.annotations {
			if annotation.label == nil {
				svg.WriteStrings(&body, `<text class="pele-annotation" x="0" y="`, num(y+tinyHeight/2+float64(tiny*0.35)), `" text-anchor="middle" font-size="`, js.Num(tiny), `"`, muted, `>`, svg.Esc(annotation.text), `</text>`)
				y += tinyHeight
			} else {
				h := math.Max(tinyHeight, annotation.label.Height)
				body.WriteString(svg.LabelSvg(*annotation.label, 0, y+h/2, ` class="pele-annotation" font-size="`+js.Num(tiny)+`"`+muted, options.Icons))
				y += h
			}
		}
		titleSize := ""
		if view.fontSize != size {
			titleSize = ` font-size="` + num(view.fontSize) + `"`
		}
		body.WriteString(svg.LabelSvg(view.label, 0, (y+first-TITLE_PAD)/2, ` class="pele-label"`+titleSize+` font-weight="var(--_hw)"`+ink+style.Text, icons))
		lines := func(out *strings.Builder, list []Line, kind string, from float64) {
			baseline := from + SECTION_PAD + lineHeight/2 + float64(memberSize*0.35)
			for _, line := range list {
				var mark string
				switch line.classifier {
				case "$":
					if style.Text != "" {
						mark = ` pele-static"` + style.Text[:len(style.Text)-1] + `text-decoration:underline"`
					} else {
						mark = ` pele-static" style="text-decoration:underline"`
					}
				case "*":
					mark = ` pele-abstract" font-style="italic"` + style.Text
				default:
					mark = `"` + style.Text
				}
				out.WriteString(`<text class="pele-member pele-` + kind + mark + ` x="` + num(left+PAD_X) + `" y="` + num(baseline) + `">` + svg.Esc(line.text) + `</text>`)
				baseline += lineHeight
			}
		}
		if len(view.attributes) > 0 || len(view.methods) > 0 {
			family := ""
			if mono(style) {
				family = ` font-family="` + svg.FONT_MONO + `"`
			}
			svg.WriteStrings(&body, `<g class="pele-members" font-size="`, js.Num(memberSize), `"`, ink, family, `>`)
			lines(&body, view.attributes, "attribute", first)
			lines(&body, view.methods, "method", second)
			body.WriteString(`</g>`)
		}
		out := body.String()
		if cls.Tooltip != "" {
			out = `<title>` + svg.EscText(cls.Tooltip) + `</title>` + out
		}
		if cls.Link != "" {
			target := ""
			if cls.LinkTarget != "" && isTarget(cls.LinkTarget) {
				target = ` target="` + cls.LinkTarget + `"`
			}
			out = util.Linked(out, cls.Link, cls.ID, &links, options, target)
		}
		return `<g class="pele-node pele-class` + svg.ClassNames(stripDefault(cls.CssClasses)) + `" data-id="` + id + `"` + at + `>` + out + `</g>`
	}

	var clusters, struckTitles, nodesOut strings.Builder
	// A class takes about 500 bytes of markup and a relation about 400.
	nodesOut.Grow(len(views) * 512)
	for i, view := range views {
		c := cnodes[i]
		node := view.node
		x := c.X + ox
		y := c.Y + oy
		id := svg.EscText(node.ID)
		w, h := view.w, view.h
		if c.IsGroup {
			w, h = c.W, c.H
		}
		if node.IsGroup {
			titleX := c.X
			if c.IsGroup {
				titleX = svg.ClusterTitleX(c, view.label.Width, crossings, GROUP_PAD)
			}
			titleY := y - h/2 + 8 + view.label.Height/2
			const titleAttrs = ` class="pele-cluster-label" fill="var(--_m)"`
			struck := c.IsGroup && svg.TitleCrossed(c, view.label.Width, titleX, crossings)
			if struck {
				struckTitles.WriteString(svg.StruckTitle(view.label, titleX+ox, titleY, titleAttrs, id, parents, i, nil))
			}
			clusters.WriteString(`<g class="pele-cluster pele-namespace" data-id="` + id + `">` +
				`<rect x="` + num(x-w/2) + `" y="` + num(y-h/2) + `" width="` + num(w) + `" height="` + num(h) + `" fill="var(--_a)" fill-opacity="0.5" stroke="var(--_b)"` + svg.ROUNDED + `/>`)

			if !struck {
				clusters.WriteString(svg.LabelSvg(view.label, titleX+ox, titleY, titleAttrs, nil))
			}
			clusters.WriteString(`</g>`)
			continue
		}
		at := ` transform="translate(` + num(x) + `,` + num(y) + `)"`
		if node.ClassNode != nil {
			nodesOut.WriteString(classSvg(view, node.ClassNode, id, at))
		} else if node.Shape == "note" {
			r := w / 2
			t := -h / 2
			nodesOut.WriteString(`<g class="pele-node pele-note" data-id="` + id + `"` + at + `>` +
				`<path d="M` + num(-r) + `,` + num(t) + `H` + num(r-FOLD) + `L` + num(r) + `,` + num(t+FOLD) + `V` + num(-t) + `H` + num(-r) + `Z" fill="var(--_a)" stroke="var(--_b)" stroke-linejoin="round"/>` +
				`<path d="M` + num(r-FOLD) + `,` + num(t) + `V` + num(t+FOLD) + `H` + num(r) + `" fill="none" stroke="var(--_b)" stroke-linejoin="round"/>` +
				svg.LabelSvg(view.label, 0, 0, ` class="pele-label" font-size="`+js.Num(small)+`"`, icons) +
				`</g>`)
		} else {
			nodesOut.WriteString(`<g class="pele-node pele-interface" data-id="` + id + `"` + at + `>` +
				svg.LabelSvg(view.label, 0, 0, ` class="pele-label" font-size="`+js.Num(small)+`"`, icons) +
				`</g>`)
		}
	}

	var geometry *types.Geometry
	if options.Geometry != nil {
		// Taken before the routes move by the offset below.
		geometry = classGeometry(db, graph, views, cnodes, cedges, drawn, edgeLabels, hideEmpty, measurer, size, maxX-minX, maxY-minY)
	}

	var edgesOut, labelsOut strings.Builder
	edgesOut.Grow(len(cedges) * 400)
	for i, ce := range cedges {
		edge := drawn[i]
		label := edgeLabels[i]
		startType := edge.ArrowTypeStart
		endType := edge.ArrowTypeEnd
		startTrim := classMarkerTrim(startType)
		endTrim := classMarkerTrim(endType)
		var path svg.EdgePath
		if ce.Src == ce.Dst {
			c := cnodes[ce.Src]
			view := views[ce.Src]
			k := float64(loopAt[i])
			x := c.X + ox + view.w/2
			y := c.Y + oy
			spread := math.Min(view.h/2-4, 8+float64(k*6))
			reach := LOOP + float64(k*8)
			path = svg.EdgePath{
				D: `M` + num(x+startTrim) + `,` + num(y-spread) + `C` + num(x+reach+startTrim) + `,` + num(y-spread) + ` ` +
					num(x+reach+endTrim) + `,` + num(y+spread) + ` ` + num(x+endTrim) + `,` + num(y+spread),
				Sx:  x,
				Sy:  y - spread,
				Sdx: -1,
				Sdy: 0,
				Ex:  x,
				Ey:  y + spread,
				Edx: -1,
				Edy: 0,
			}
		} else {
			route := ce.Route
			for k := 0; k+1 < len(route); k += 3 {
				route[k] += ox
				route[k+1] += oy
			}
			path = svg.RoutePath(route, "", startTrim, endTrim, svg.DefaultReach(startTrim), svg.DefaultReach(endTrim))
		}
		attrs := ""
		if edge.Pattern == "dashed" {
			attrs = ` stroke-dasharray="6 4"`
		} else if edge.Pattern == "dotted" {
			attrs = ` stroke-dasharray="2 4"`
		}
		kind := ` pele-relation`
		if edge.Pattern == "dotted" {
			kind = ` pele-note-edge`
		}
		edgesOut.WriteString(`<g class="pele-edge` + kind + `" data-id="` + svg.EscText(edge.ID) + `">` +
			`<path d="` + path.D + `"` + attrs + `/>` +
			classMarker(startType, path.Sx, path.Sy, path.Sdx, path.Sdy, LINE) +
			classMarker(endType, path.Ex, path.Ey, path.Edx, path.Edy, LINE) +
			`</g>`)
		if label.Width > 0 {
			x := ce.LabelX + ox
			y := ce.LabelY + oy
			labelsOut.WriteString(svg.EdgeLabelSvg(edge.ID, label, x, y, "", icons))
		}
	}

	var cardsOut strings.Builder
	for _, item := range cards {
		if item.math != nil {
			cardsOut.WriteString(svg.LabelSvg(*item.math, item.x+ox, item.y+oy, ` class="pele-cardinality"`, options.Icons))
		} else {
			cardsOut.WriteString(`<text class="pele-cardinality" x="` + num(item.x+ox) + `" y="` + num(item.y+oy+float64(tiny*0.35)) + `" text-anchor="middle">` + svg.Esc(item.text) + `</text>`)
		}
	}

	content := make([]string, 0, 32)
	if clusters.Len() > 0 {
		content = append(content, `<g class="pele-clusters">`, clusters.String(), `</g>`)
	}
	if edgesOut.Len() > 0 {
		content = append(content, `<g class="pele-edges" fill="none" stroke="`+LINE+`" stroke-linecap="round">`, edgesOut.String(), `</g>`)
	}
	if struckTitles.Len() > 0 {
		content = append(content, `<g class="pele-cluster-titles">`, struckTitles.String(), `</g>`)
	}
	if labelsOut.Len() > 0 {
		content = append(content, `<g class="pele-edge-labels" font-size="`+js.Num(small)+`">`, labelsOut.String(), `</g>`)
	}
	if cardsOut.Len() > 0 {
		content = append(content, `<g class="pele-cardinalities" font-size="`+js.Num(tiny)+`" fill="var(--_m)">`, cardsOut.String(), `</g>`)
	}
	content = append(content, `<g class="pele-nodes">`, nodesOut.String(), `</g>`)
	titled := svg.WithTitle(db.Title, measurer, size, pad, width, height, content...)
	out := svg.SvgDocument("class", titled.Width, titled.Height, size, options, svg.Accessible{AccTitle: db.AccTitle, AccDescr: db.AccDescr}, titled.Content...)

	return types.Rendered{SVG: out, Width: titled.Width, Height: titled.Height, Links: links, Geometry: geometry}
}

// classGeometry is the layout in SVG units, for drawing the diagram some other way.
func classGeometry(db *ClassDb, graph ClassGraph, views []*View, cnodes []*layout.CNode, cedges []*layout.CEdge, drawn []*GraphEdge, edgeLabels []text.Label,
	hideEmpty bool, measurer text.TextMeasurer, size, width, height float64) *types.Geometry {
	g := &types.Geometry{Width: width, Height: height, Title: svg.TitleLabel(db.Title, measurer, size, 4000).Plain()}
	lines := func(list []Line) []string {
		out := make([]string, len(list))
		for i, l := range list {
			out[i] = l.text
		}
		return out
	}
	for i, c := range cnodes {
		v := views[i]
		node := graph.Nodes[i]
		n := types.GeoNode{ID: node.ID, Shape: node.Shape, X: c.X, Y: c.Y, W: v.w, H: v.h, Label: v.label.Plain(), Group: c.IsGroup, Parent: c.Parent}
		switch {
		case c.IsGroup:
			n.Shape, n.W, n.H = "namespace", c.W, c.H
		case node.IsGroup:
			n.Shape = "rect"
		case node.ClassNode != nil:
			n.Shape = "class"
			annotations := make([]string, len(v.annotations))
			for k, a := range v.annotations {
				annotations[k] = a.text
			}
			n.Label = append(annotations, n.Label...)
			if !hideEmpty || len(v.attributes) > 0 || len(v.methods) > 0 {
				n.Sections = [][]string{lines(v.attributes), lines(v.methods)}
			}
		case node.Shape == "interface":
			n.Shape = "text"
		}
		g.Nodes = append(g.Nodes, n)
	}
	for i, ce := range cedges {
		edge := drawn[i]
		e := types.GeoEdge{From: ce.Src, To: ce.Dst, Route: append([]float64{}, ce.Route...), Start: edge.ArrowTypeStart, End: edge.ArrowTypeEnd, Stroke: "normal",
			Label: edgeLabels[i].Plain(), LabelX: ce.LabelX, LabelY: ce.LabelY,
			StartText: text.DecodeEntities(edge.StartLabelRight), EndText: text.DecodeEntities(edge.EndLabelLeft)}
		if edge.Pattern == "dashed" || edge.Pattern == "dotted" {
			e.Stroke = "dotted"
		}
		g.Edges = append(g.Edges, e)
	}
	return g
}

// endSide is svg.EndSide, and -1 off the ends of a route, as pele's reads of undefined give.
func endSide(route []float64, at int, c *layout.CNode) int {
	if at < 0 || at+1 >= len(route) {
		return -1
	}
	return svg.EndSide(route, at, c)
}
