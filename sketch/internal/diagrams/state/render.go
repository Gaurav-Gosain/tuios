// Ported from pele src/diagrams/state/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package state

import (
	"math"
	"slices"
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
	NODE_SEP   = 40
	EDGE_SEP   = 16
	RANK_SEP   = 44
	GROUP_PAD  = 16
	REGION_PAD = 10
	PAD_X      = 16
	PAD_Y      = 10
	LOOP       = 26
	FOLD       = 8
	NOTE_WRAP  = 320
	CORNER     = 10
	BAR        = 8
	SURFACE    = ` fill="var(--_s)" stroke="var(--_b)"`
	RULE       = ` fill="none" stroke="var(--_b)"`
)

var mermaidClassNames = [...]string{"state", "cluster-alt", "cluster", "note"}

// MERMAID_CLASSES: /(?:^| )statediagram-(?:state|cluster-alt|cluster|note)(?= |$)/g. Go's regexp
// has no lookahead, so the match is written out.
func stripMermaidClasses(s string) string {
	matchFrom := func(start int) int {
		if !strings.HasPrefix(s[start:], "statediagram-") {
			return -1
		}
		at := start + len("statediagram-")
		for _, name := range mermaidClassNames {
			end := at + len(name)
			if strings.HasPrefix(s[at:], name) && (end == len(s) || s[end] == ' ') {
				return end
			}
		}
		return -1
	}
	var b strings.Builder
	last := 0
	for i := 0; i < len(s); {
		end := -1
		if i == 0 {
			end = matchFrom(0)
		}
		if end == -1 && s[i] == ' ' {
			end = matchFrom(i + 1)
		}
		if end == -1 {
			i++
			continue
		}
		b.WriteString(s[last:i])
		last = end
		i = end
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// K is the kind of a drawn item.
type K int

const (
	K_State K = iota
	K_Described
	K_Start
	K_End
	K_Choice
	K_Fork
	K_Join
	K_Note
	K_Composite
	K_Region
	K_Hidden
)

var KINDS = map[string]K{
	"rectWithTitle": K_Described,
	"stateStart":    K_Start,
	"stateEnd":      K_End,
	"choice":        K_Choice,
	"fork":          K_Fork,
	"join":          K_Join,
	"note":          K_Note,
	"divider":       K_Hidden,
}

var CLASSES = [...]string{
	"pele-node pele-state",
	"pele-node pele-state",
	"pele-node pele-state-start",
	"pele-node pele-state-end",
	"pele-node pele-state-choice",
	"pele-node pele-state-fork",
	"pele-node pele-state-join",
	"pele-note",
	"pele-cluster pele-state-composite",
}

type view struct {
	// nil for the unseen helpers the layout needs.
	node   *StateNode
	kind   K
	style  *svg.ResolvedStyle
	label  text.Label
	lines  []text.Label
	w      float64
	h      float64
	titleH float64
}

func vertical(dir layout.Dir) bool {
	return dir == "TB" || dir == "BT"
}

func numberOption(config types.Config, key string, fallback float64) float64 {
	section, _ := config["state"].(map[string]any)
	if value, ok := section[key].(float64); ok && value > 0 {
		return math.Min(value, 2000)
	}
	return fallback
}

// Mermaid keeps the quotes of a click statement's strings and strips them when it draws.
func unquote(text string) string {
	from := 0
	to := len(text)
	for from < to && text[from] == 34 {
		from++
	}
	for to > from && text[to-1] == 34 {
		to--
	}
	return text[from:to]
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

func rect(x, y, w, h float64, rest string) string {
	return `<rect x="` + svg.Num(x) + `" y="` + svg.Num(y) + `" width="` + svg.Num(w) + `" height="` + svg.Num(h) + `"` + rest + `/>`
}

func group(dir layout.Dir, padX, padTop, padBottom float64) *layout.CNode {
	c := layout.Cnode(0, 0, -1)
	c.IsGroup = true
	c.Dir = dir
	c.PadX = padX
	c.PadTop = padTop
	c.PadBottom = padBottom
	return c
}

func renderState(db *StateDb, config types.Config, options types.RenderOptions) types.Rendered {
	if !common.RunsAcross(db.Direction()) {
		return draw(db, config, options, false)
	}
	return common.TurnToFit(options, func(down bool) types.Rendered { return draw(db, config, options, down) })
}

// draw: `turned` draws a diagram that runs across as one that runs down.
func draw(db *StateDb, config types.Config, options types.RenderOptions, turned bool) types.Rendered {
	graph := db.Graph
	// State diagrams follow the flowchart curve.
	flow, _ := config["flowchart"].(map[string]any)
	curve, _ := flow["curve"].(string)
	size := types.Or(options.FontSize, 16)
	small := js.Round(size * 0.875)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	wrapWidth := numberOption(config, "wrappingWidth", 200)
	pad := types.Or(options.Padding, 8)
	icons := options.Icons
	rootDir := layout.Direction(db.Direction())
	if turned {
		rootDir = "TB"
	}
	noLabel := text.LayoutLabel("", false, measurer, size, 0, 0)
	label := func(raw string, fontSize, wrap float64, style int) text.Label {
		return text.LayoutLabel(raw, true, measurer, fontSize, wrap, style)
	}

	index := map[string]int{}
	var views []*view
	var cnodes []*layout.CNode
	add := func(node *StateNode, kind K, c *layout.CNode) int {
		views = append(views, &view{node: node, kind: kind, label: noLabel})
		if c != nil {
			cnodes = append(cnodes, c)
		}
		return len(views) - 1
	}
	for _, node := range graph.Nodes {
		if node.Shape == "noteGroup" {
			continue
		}
		var kind K
		if node.IsGroup {
			if node.Shape == "divider" {
				kind = K_Region
			} else {
				kind = K_Composite
			}
		} else if k, ok := KINDS[node.Shape]; ok {
			kind = k
		} else {
			kind = K_State
		}
		index[node.ID] = add(node, kind, nil)
	}
	states := len(views)

	logical := make([]int, 0, states)
	for i := 0; i < states; i++ {
		parent := -1
		if parentId := views[i].node.ParentID; parentId != nil {
			if p, ok := index[*parentId]; ok && p != i && views[p].node.IsGroup {
				parent = p
			}
		}
		logical = append(logical, parent)
	}
	// Composites that are redeclared inside each other form a ring with no way in from the root.
	// One member of each ring moves to the root, which leaves a tree.
	{
		kids := make([][]int, states)
		var queue []int
		placed := make([]bool, states)
		for i := 0; i < states; i++ {
			if logical[i] == -1 {
				placed[i] = true
				queue = append(queue, i)
			} else {
				kids[logical[i]] = append(kids[logical[i]], i)
			}
		}
		done := 0
		settle := func() {
			for ; done < len(queue); done++ {
				for _, kid := range kids[queue[done]] {
					if placed[kid] {
						continue
					}
					placed[kid] = true
					queue = append(queue, kid)
				}
			}
		}
		settle()
		seen := make([]int, states)
		for i := range seen {
			seen[i] = -1
		}
		for i := 0; i < states; i++ {
			if placed[i] {
				continue
			}
			ring := i
			for seen[ring] != i {
				seen[ring] = i
				ring = logical[ring]
			}
			logical[ring] = -1
			placed[ring] = true
			queue = append(queue, ring)
			settle()
		}
	}
	owners := make([]int, states)
	for i := 0; i < states; i++ {
		owners[i] = -1
		noteFor := views[i].node.NoteFor
		if noteFor == nil {
			continue
		}
		owner, ok := index[*noteFor]
		if !ok {
			continue
		}
		owners[i] = owner
		logical[i] = logical[owner]
	}

	// As in Mermaid, a composite that sets no direction runs top to bottom whatever surrounds it.
	innerDir := func(composite int) layout.Dir { return layout.Direction(views[composite].node.Dir) }
	// Whether the flow around a state runs top to bottom or bottom to top.
	across := func(i int) bool {
		if logical[i] == -1 {
			return vertical(rootDir)
		}
		return vertical(innerDir(logical[i]))
	}

	degree := make([]int, states)
	noted := make([]bool, states)
	// When each state first takes part in a transition. Layout follows this order rather than
	// the order of declaration, so a state declared up front does not become the top of the flow.
	appear := make([]float64, states)
	for i := range appear {
		appear[i] = math.Inf(1)
	}
	appeared := 0.0
	counts := make([]int, 2*states)
	for _, edge := range graph.Edges {
		src, ok1 := index[edge.Start]
		dst, ok2 := index[edge.End]
		if !ok1 || !ok2 {
			continue
		}
		if edge.Pattern == "dashed" {
			if views[src].kind == K_Note {
				noted[dst] = true
			} else {
				noted[src] = true
			}
			continue
		}
		counts[2*src]++
		degree[src] = max(degree[src], counts[2*src])
		counts[2*dst+1]++
		degree[dst] = max(degree[dst], counts[2*dst+1])
		if math.IsInf(appear[src], 1) {
			appear[src] = appeared
			appeared++
		}
		if math.IsInf(appear[dst], 1) {
			appear[dst] = appeared
			appeared++
		}
	}

	for i := 0; i < states; i++ {
		v := views[i]
		node := v.node
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
		fontSize := size
		if style.FontSize != 0 {
			fontSize = style.FontSize
		}
		v.style = &style
		var c *layout.CNode
		switch v.kind {
		case K_Composite:
			v.label = label(node.Label, fontSize, 4000, textStyle)
			if v.label.Height > 0 {
				v.titleH = v.label.Height + 12
			}
			c = group(innerDir(i), GROUP_PAD, v.titleH+GROUP_PAD, GROUP_PAD)
			c.MinW = v.label.Width + 2*GROUP_PAD
		case K_Region:
			c = group(innerDir(i), REGION_PAD, REGION_PAD, REGION_PAD)
		case K_Start:
			v.w, v.h = 14, 14
		case K_End:
			v.w, v.h = 18, 18
		case K_Choice:
			v.w, v.h = 30, 30
		case K_Fork, K_Join:
			length := clamp(float64(40*float64(degree[i]))+16, 72, 320)
			if across(i) {
				v.w, v.h = length, BAR
			} else {
				v.w, v.h = BAR, length
			}
		case K_Hidden:
		case K_Note:
			v.label = label(node.Label, small, math.Max(wrapWidth, NOTE_WRAP), 0)
			v.w = v.label.Width + 24
			v.h = v.label.Height + 16
		default:
			v.label = label(node.Label, fontSize, wrapWidth, textStyle)
			width := v.label.Width
			height := v.label.Height + 2*PAD_Y
			if v.kind == K_Described {
				height += 14
				for _, line := range node.Description {
					if line == "" {
						line = " "
					}
					l := label(line, small, wrapWidth, textStyle)
					v.lines = append(v.lines, l)
					if l.Width > width {
						width = l.Width
					}
					height += l.Height
				}
			}
			v.w = math.Max(width+2*PAD_X, 56)
			v.h = height
		}
		if c == nil {
			c = layout.Cnode(v.w, v.h, -1)
		}
		// The transitions on one side of a state's box spread along it, clear of its corners.
		if v.kind == K_State || v.kind == K_Described {
			if across(i) {
				c.Span = v.w - 24
			} else {
				c.Span = v.h - 24
			}
		}
		cnodes = append(cnodes, c)
	}

	var cedges []*layout.CEdge
	var edgeLabels []text.Label
	var drawn []StateEdge
	// The transitions each state makes to itself: how many, their widest label, and their labels' height.
	loopCount := make([]int, states)
	loopWidth := make([]float64, states)
	loopHeight := make([]float64, states)
	// A transition between a composite and a state inside it runs between that state and a point
	// just inside the composite, and is then drawn on to the composite's border.
	anchors := map[int]int{}
	encloses := func(composite, item int) bool {
		if !cnodes[composite].IsGroup {
			return false
		}
		for p := logical[item]; p != -1; p = logical[p] {
			if p == composite {
				return true
			}
		}
		return false
	}
	anchor := func(composite int, first bool) int {
		anchors[len(cedges)] = composite
		if first {
			appear = append(appear, -1)
		} else {
			appear = append(appear, math.Inf(1))
		}
		return add(nil, K_Hidden, layout.Cnode(0, 0, composite))
	}
	for _, edge := range graph.Edges {
		src, ok1 := index[edge.Start]
		dst, ok2 := index[edge.End]
		if !ok1 || !ok2 {
			continue
		}
		l := label(edge.Label, small, wrapWidth, 0)
		labelW := 0.0
		if l.Width > 0 {
			labelW = l.Width + 12
		}
		if src == dst {
			loopCount[src]++
			loopWidth[src] = math.Max(loopWidth[src], labelW)
			loopHeight[src] += l.Height
		} else if encloses(src, dst) {
			src = anchor(src, true)
		} else if encloses(dst, src) {
			dst = anchor(dst, false)
		}
		drawn = append(drawn, edge)
		edgeLabels = append(edgeLabels, l)
		labelH := 0.0
		if l.Height > 0 {
			labelH = l.Height + 4
		}
		cedges = append(cedges, &layout.CEdge{Src: src, Dst: dst, Minlen: 1, LabelW: labelW, LabelH: labelH, Route: []float64{}})
	}

	// A state with notes, or a composite with self-transitions, sits in an unseen group of its
	// own: the notes go beside the state, across the flow, without disturbing its transitions.
	for i := 0; i < states; i++ {
		if owners[i] != -1 {
			continue
		}
		item := cnodes[i]
		// Loops hang off the right, or below when the flow is horizontal, with their labels beyond them.
		room := 0.0
		if loopCount[i] > 0 {
			reach := float64(LOOP + (loopCount[i]-1)*8)
			if across(i) {
				room = reach + loopWidth[i]
			} else {
				room = float64(reach*0.75) + 4 + loopHeight[i]
			}
			if !item.IsGroup {
				if across(i) {
					item.W += float64(2 * room)
					item.H = math.Max(item.H, loopHeight[i])
				} else {
					item.H += float64(2 * room)
					item.W = math.Max(item.W, loopWidth[i])
				}
				room = 0
			}
		}
		if !noted[i] && room == 0 {
			item.Parent = logical[i]
			continue
		}
		var c *layout.CNode
		if across(i) {
			c = group("LR", room, 0, 0)
		} else {
			c = group("TB", 0, room, room)
		}
		c.Parent = logical[i]
		appear = append(appear, appear[i])
		cnodes[i].Parent = add(nil, K_Hidden, c)
	}
	for i := 0; i < states; i++ {
		if owners[i] != -1 {
			cnodes[i].Parent = cnodes[owners[i]].Parent
		}
	}

	{
		// Numbers the tree so that, at every level, initial states come first and the rest follow
		// in the order the transitions introduce them.
		members := make([][]int, len(cnodes))
		var top []int
		for i := 0; i < len(cnodes); i++ {
			p := cnodes[i].Parent
			if p >= 0 {
				members[p] = append(members[p], i)
			} else {
				top = append(top, i)
			}
		}
		initial := func(i int) int {
			if views[i].kind == K_Start {
				return 0
			}
			return 1
		}
		byFlow := func(a, b int) int {
			if d := initial(a) - initial(b); d != 0 {
				return d
			}
			if appear[a] != appear[b] {
				if appear[a] < appear[b] {
					return -1
				}
				return 1
			}
			return a - b
		}
		seq := 0
		slices.SortStableFunc(top, byFlow)
		slices.Reverse(top)
		pending := top
		for len(pending) > 0 {
			i := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			cnodes[i].Seq = seq
			seq++
			inside := members[i]
			slices.SortStableFunc(inside, byFlow)
			for k := len(inside) - 1; k >= 0; k-- {
				pending = append(pending, inside[k])
			}
		}
	}

	lay := layout.CompoundLayout(cnodes, cedges, rootDir, layout.LayeredOptions{
		NodeSep:  numberOption(config, "nodeSpacing", NODE_SEP),
		EdgeSep:  EDGE_SEP,
		RankSep:  numberOption(config, "rankSpacing", RANK_SEP),
		PortSep:  20,
		Tracks:   curve == "step" || curve == "stepBefore" || curve == "stepAfter",
		Straight: curve == "linear",
		HeadRoom: svg.MarkerTrim("arrow_point"),
	})

	width := math.Ceil(lay.Width + float64(2*pad))
	height := math.Ceil(lay.Height + float64(2*pad))
	var links []types.LinkInfo

	// Route points by rounded y, to find the edges that cross a composite's title bar.
	crossings := map[float64][]float64{}
	for _, e := range cedges {
		route := e.Route
		for k := 3; k < len(route)-3; k += 3 {
			key := js.Round(route[k+1])
			crossings[key] = append(crossings[key], route[k])
		}
	}

	children := make([][]int, len(cnodes))
	var order []int
	for i := 0; i < len(cnodes); i++ {
		p := cnodes[i].Parent
		if p >= 0 {
			children[p] = append(children[p], i)
		} else {
			order = append(order, i)
		}
	}
	roots := append([]int{}, order...)
	for q := 0; q < len(order); q++ {
		order = append(order, children[order[q]]...)
	}

	var clusters, dividers, nodesOut, notesOut strings.Builder
	var fills []svg.Fill
	// A state takes about 230 bytes of markup and a transition about 300.
	nodesOut.Grow(len(cnodes) * 232)
	// Draws the dashed lines between the regions among a composite's members.
	divide := func(items []int, left, top, right, bottom float64) {
		var regions []int
		for _, i := range items {
			if views[i].kind == K_Region {
				regions = append(regions, i)
			}
		}
		if len(regions) < 2 {
			return
		}
		lo := func(i int, x bool) float64 {
			if x {
				return cnodes[i].X - cnodes[i].W/2
			}
			return cnodes[i].Y - cnodes[i].H/2
		}
		hi := func(i int, x bool) float64 {
			if x {
				return cnodes[i].X + cnodes[i].W/2
			}
			return cnodes[i].Y + cnodes[i].H/2
		}
		for _, x := range [...]bool{true, false} {
			sorted := append([]int{}, regions...)
			slices.SortStableFunc(sorted, func(a, b int) int {
				d := lo(a, x) - lo(b, x)
				if d < 0 {
					return -1
				}
				if d > 0 {
					return 1
				}
				return a - b
			})
			apart := true
			for k := 1; k < len(sorted); k++ {
				if hi(sorted[k-1], x) > lo(sorted[k], x)+0.5 {
					apart = false
				}
			}
			if !apart {
				continue
			}
			for k := 1; k < len(sorted); k++ {
				at := svg.Num((hi(sorted[k-1], x)+lo(sorted[k], x))/2 + pad)
				if x {
					dividers.WriteString(`<path class="pele-divider" d="M` + at + `,` + svg.Num(top) + `V` + svg.Num(bottom) + `"/>`)
				} else {
					dividers.WriteString(`<path class="pele-divider" d="M` + svg.Num(left) + `,` + at + `H` + svg.Num(right) + `"/>`)
				}
			}
			return
		}
		// Regions that sit neither side by side nor in a stack each get an outline.
		for _, i := range regions {
			c := cnodes[i]
			dividers.WriteString(rect(c.X-c.W/2+pad, c.Y-c.H/2+pad, c.W, c.H, ` class="pele-divider" rx="`+js.Int(CORNER)+`"`))
		}
	}
	divide(roots, pad/2, pad/2, width-pad/2, height-pad/2)

	for _, i := range order {
		v := views[i]
		node := v.node
		style := v.style
		if node == nil || style == nil || v.kind >= K_Region {
			continue
		}
		c := cnodes[i]
		x := c.X + pad
		y := c.Y + pad
		w := v.w
		h := v.h
		ink := svg.InkFill(*style, "")
		labelAttrs := ` class="pele-label"` + ink + style.Text
		var body string

		switch v.kind {
		case K_Composite:
			left := x - c.W/2
			top := y - c.H/2
			if svg.Paints(style.Shape) {
				fills = append(fills, svg.Fill{Box: [4]float64{left, top, left + c.W, top + c.H}, Shape: style.Shape})
			}
			wash := ` fill-opacity="0.5"`
			if svg.SetsFill(style.Shape) {
				wash = ""
			}
			body = rect(left, top, c.W, c.H, ` rx="`+js.Int(CORNER)+`" fill="var(--_a)"`+wash+` stroke="var(--_b)"`+style.Shape)
			if v.titleH > 0 {
				body += `<path d="M` + svg.Num(left) + `,` + svg.Num(top+v.titleH) + `H` + svg.Num(left+c.W) + `"` + RULE + style.Line + `/>` +
					svg.LabelSvg(
						v.label,
						titleX(c, v.label.Width, crossings)+pad,
						top+v.titleH/2,
						` class="pele-cluster-label"`+ink+style.Text,
						icons,
					)
			}
			divide(children[i], left, top+v.titleH, left+c.W, top+c.H)
		case K_Start:
			body = `<circle r="7" fill="var(--_l)"` + style.Shape + `/>`
		case K_End:
			body = `<circle r="8.5" fill="var(--_bg)" stroke="var(--_l)"` + style.Line + `/><circle r="5" fill="var(--_l)"` + style.Shape + `/>`
		case K_Choice:
			body = `<polygon points="0,` + svg.Num(-h/2) + ` ` + svg.Num(w/2) + `,0 0,` + svg.Num(h/2) + ` ` + svg.Num(-w/2) + `,0"` + SURFACE + style.Shape + `/>`
		case K_Fork, K_Join:
			body = rect(-w/2, -h/2, w, h, ` rx="2" fill="var(--_l)"`+style.Shape)
		case K_Note:
			r := svg.Num(w / 2)
			b := svg.Num(h / 2)
			fold := svg.Num(w/2 - FOLD)
			corner := svg.Num(FOLD - h/2)
			body = `<path d="M-` + r + `,-` + b + `H` + fold + `L` + r + `,` + corner + `V` + b + `H-` + r + `Z" fill="var(--_bg)" stroke="var(--_b)"/>` +
				`<path d="M` + fold + `,-` + b + `V` + corner + `H` + r + `"` + RULE + `/>` +
				svg.LabelSvg(v.label, 0, 0, ` class="pele-label" font-size="`+js.Num(small)+`" fill="var(--_m)"`, icons)
		default:
			body = rect(-w/2, -h/2, w, h, ` rx="`+svg.Num(math.Min(CORNER, h/2))+`"`+SURFACE+style.Shape)
			if v.kind == K_State {
				body += svg.LabelSvg(v.label, 0, 0, labelAttrs, icons)
				break
			}
			at := PAD_Y - h/2 + v.label.Height
			body += `<path d="M` + svg.Num(-w/2) + `,` + svg.Num(at+6) + `H` + svg.Num(w/2) + `"` + RULE + style.Line + `/>` + svg.LabelSvg(v.label, 0, at-v.label.Height/2, labelAttrs, icons)
			at += 14
			// One Builder, not a += per line: Go copies the string on each +=, where JavaScript ropes do not.
			var lines strings.Builder
			lines.WriteString(body)
			for _, line := range v.lines {
				lines.WriteString(svg.LabelSvg(line, 0, at+line.Height/2, ` class="pele-state-description" font-size="`+js.Num(small)+`"`+ink+style.Text, icons))
				at += line.Height
			}
			body = lines.String()
		}

		if link, ok := db.Links[node.ID]; ok {
			// Entity codes are resolved first, so the URL is checked in the form a browser would follow.
			tooltip := text.DecodeEntities(unquote(link.Tooltip))
			if tooltip != "" {
				body = `<title>` + svg.Esc(tooltip) + `</title>` + body
			}
			body = util.Linked(body, util.SafeUrl(unquote(link.URL)), node.ID, &links, options, ` target="_blank"`)
		}
		open := `<g class="` + CLASSES[v.kind] + svg.ClassNames(stripMermaidClasses(node.CssClasses)) + `" data-id="` + svg.EscText(node.ID) + `"`
		switch v.kind {
		case K_Composite:
			clusters.WriteString(open + `>` + body + `</g>`)
		case K_Note:
			notesOut.WriteString(open + ` transform="translate(` + svg.Num(x) + `,` + svg.Num(y) + `)">` + body + `</g>`)
		default:
			nodesOut.WriteString(open + ` transform="translate(` + svg.Num(x) + `,` + svg.Num(y) + `)">` + body + `</g>`)
		}
	}

	// Moves a route end from the layout box onto the drawn shape, and spreads the ends along a bar.
	attach := func(route []float64, at int, i int) {
		c := cnodes[i]
		if c.IsGroup {
			return
		}
		v := views[i]
		bar := v.kind == K_Fork || v.kind == K_Join
		other := at - 3
		if at == 0 {
			other = 3
		}
		// 0 is the x axis and 1 the y axis; the end sits on a side that faces along `axis`.
		for _, axis := range [...]int{1, 0} {
			extent, drawnExtent, breadth, center, cross := c.W, v.w, v.h, c.X+pad, c.Y+pad
			if axis == 1 {
				extent, drawnExtent, breadth, center, cross = c.H, v.h, v.w, c.Y+pad, c.X+pad
			}
			delta := route[at+axis] - center
			if math.Abs(math.Abs(delta)-extent/2) >= 0.5 {
				continue
			}
			route[at+axis] -= (js.Sign(delta) * (extent - drawnExtent)) / 2
			if bar && breadth > drawnExtent {
				route[at+1-axis] = clamp(route[other+1-axis], cross-breadth/2+6, cross+breadth/2-6)
			}
			return
		}
	}

	reach := svg.NewSideReach()
	for i, ce := range cedges {
		if ce.Src == ce.Dst || len(ce.Route) < 6 || drawn[i].Pattern == "dashed" {
			continue
		}
		reach.Add(ce.Dst, svg.EndSide(ce.Route, len(ce.Route)-3, cnodes[ce.Dst]), "arrow_point")
	}

	var casings, edgesOut, labelsOut strings.Builder
	edgesOut.Grow(len(cedges) * 304)
	loopSeen := make([]int, states)
	loopText := make([]float64, states)
	for i, ce := range cedges {
		edge := drawn[i]
		l := edgeLabels[i]
		note := edge.Pattern == "dashed"
		endType := "arrow_point"
		if note {
			endType = "none"
		}
		var path svg.EdgePath
		if ce.Src == ce.Dst {
			a := cnodes[ce.Src]
			v := views[ce.Src]
			k := float64(loopSeen[ce.Src])
			loopSeen[ce.Src]++
			// The loop hangs off a side the flow does not use: the right, or below when the flow is horizontal.
			side := across(ce.Src)
			w, h := v.w, v.h
			if a.IsGroup {
				w, h = a.W, a.H
			}
			out := h / 2
			spreadBase := w/2 - 4
			if side {
				out = w / 2
				spreadBase = h/2 - 4
			}
			spread := math.Max(math.Min(spreadBase, 8+float64(k*6)), 2)
			reach := LOOP + float64(k*8)
			trim := svg.MarkerTrim(endType) / js.Or(js.Hypot(reach, spread), 1)
			// Offsets are given for a loop on the right and turned a quarter for one below.
			px := func(dx, dy float64) float64 {
				if side {
					return a.X + pad + (out + dx)
				}
				return a.X + pad + (-dy)
			}
			py := func(dx, dy float64) float64 {
				if side {
					return a.Y + pad + dy
				}
				return a.Y + pad + (out + dx)
			}
			at := func(dx, dy float64) string { return svg.Num(px(dx, dy)) + `,` + svg.Num(py(dx, dy)) }
			path = svg.EdgePath{
				D:   `M` + at(0, -spread) + `C` + at(reach, -2*spread) + ` ` + at(reach, 2*spread) + ` ` + at(reach*trim, spread+float64(spread*trim)),
				Ex:  px(0, spread),
				Ey:  py(0, spread),
				Edx: px(-reach, -spread) - px(0, 0),
				Edy: py(-reach, -spread) - py(0, 0),
			}
			length := js.Or(js.Hypot(path.Edx, path.Edy), 1)
			path.Edx /= length
			path.Edy /= length
			// Labels stack beyond the outermost loop, in the order of their transitions.
			furthest := float64(LOOP + (loopCount[ce.Src]-1)*8)
			below := loopText[ce.Src] + l.Height/2
			loopText[ce.Src] += l.Height
			if side {
				ce.LabelX = a.X + out + furthest + 4 + ce.LabelW/2
				ce.LabelY = a.Y - loopHeight[ce.Src]/2 + below
			} else {
				ce.LabelX = a.X
				ce.LabelY = a.Y + out + float64(furthest*0.75) + 4 + below
			}
		} else {
			route := append([]float64{}, ce.Route...)
			leaves := reach.Of(ce.Src, endSide(route, 0, cnodes[ce.Src]))
			arrives := reach.Of(ce.Dst, endSide(route, len(route)-3, cnodes[ce.Dst]))
			for k := 0; k < len(route); k += 3 {
				route[k] += pad
				route[k+1] += pad
			}
			if len(route) >= 3 {
				attach(route, 0, ce.Src)
				attach(route, len(route)-3, ce.Dst)
			}
			if composite, ok := anchors[i]; ok && len(route) >= 6 {
				g := cnodes[composite]
				inward := views[ce.Src].kind == K_Hidden
				at := len(route) - 3
				if inward {
					at = 0
				}
				near := (g.Dir == "TB" || g.Dir == "LR") == inward
				var point []float64
				if vertical(g.Dir) {
					py := g.Y + pad + g.H/2
					if near {
						py = g.Y + pad + (views[composite].titleH - g.H/2)
					}
					point = []float64{route[at], py, 0}
				} else {
					px := g.X + pad + g.W/2
					if near {
						px = g.X + pad + (-g.W / 2)
					}
					point = []float64{px, route[at+1], 1}
				}
				if inward {
					route = append(point, route...)
				} else {
					route = append(route, point...)
				}
			}
			path = svg.RoutePath(route, curve, 0, svg.MarkerTrim(endType), leaves, arrives)
		}
		over, onFill := svg.OverFills(path.D, []float64{path.Ex, path.Ey}, fills, "", path.Tokens)
		if onFill && over == "" {
			casings.WriteString(svg.EdgeCasing(path, "none", endType, "", false))
		}
		edgesOut.WriteString(`<g class="pele-edge `)
		switch {
		case note:
			stroke := over
			if stroke == "" {
				stroke = "var(--_b)"
			}
			svg.WriteStrings(&edgesOut, `pele-note-edge" stroke="`, stroke, `" stroke-dasharray="3 4`)
		case over != "":
			svg.WriteStrings(&edgesOut, `pele-transition" stroke="`, over)
		default:
			edgesOut.WriteString(`pele-transition`)
		}
		color := over
		if color == "" {
			color = "var(--_l)"
		}
		edgesOut.WriteString(`" data-id="` + svg.EscText(edge.ID) + `">` +
			`<path d="` + path.D + `"/>` +
			svg.Marker(endType, path.Ex, path.Ey, path.Edx, path.Edy, color) +
			`</g>`)
		if l.Width > 0 {
			x := ce.LabelX + pad
			y := ce.LabelY + pad
			labelsOut.WriteString(svg.EdgeLabelSvg(edge.ID, l, x, y, "", icons))
		}
	}

	content := make([]string, 0, 32)
	if clusters.Len() > 0 {
		content = append(content, `<g class="pele-clusters">`, clusters.String(), `</g>`)
	}
	if dividers.Len() > 0 {
		content = append(content, `<g class="pele-dividers" stroke-dasharray="4 4"`+RULE+`>`, dividers.String(), `</g>`)
	}
	if casings.Len() > 0 {
		content = append(content, svg.CasingsSvg(casings.String()))
	}
	if edgesOut.Len() > 0 {
		content = append(content, `<g class="pele-edges" fill="none" stroke="var(--_l)" stroke-linecap="round">`, edgesOut.String(), `</g>`)
	}
	if labelsOut.Len() > 0 {
		content = append(content, `<g class="pele-edge-labels" font-size="`+js.Num(small)+`">`, labelsOut.String(), `</g>`)
	}
	content = append(content, `<g class="pele-nodes">`, nodesOut.String(), notesOut.String(), `</g>`)
	titled := svg.WithTitle(db.Title, measurer, size, pad, width, height, content...)
	out := svg.SvgDocument("state", titled.Width, titled.Height, size, options, svg.Accessible{AccTitle: db.AccTitle, AccDescr: db.AccDescr}, titled.Content...)

	rendered := types.Rendered{SVG: out, Width: titled.Width, Height: titled.Height, Links: links}
	if options.Geometry != nil {
		rendered.Geometry = stateGeometry(db, views, cnodes, cedges, drawn, edgeLabels, lay, measurer, size)
	}
	return rendered
}

// geoShapes name each kind of state for the geometry: sketch/text draws by them.
var geoShapes = [...]string{K_State: "state", K_Described: "state", K_Start: "state-start", K_End: "state-end", K_Choice: "choice",
	K_Fork: "fork", K_Join: "fork", K_Note: "note", K_Composite: "composite", K_Region: "region", K_Hidden: "hidden"}

// stateGeometry is the layout in SVG units, before the padding, for drawing the diagram some other way.
func stateGeometry(db *StateDb, views []*view, cnodes []*layout.CNode, cedges []*layout.CEdge, drawn []StateEdge, edgeLabels []text.Label,
	lay layout.CompoundResult, measurer text.TextMeasurer, size float64) *types.Geometry {
	g := &types.Geometry{Width: lay.Width, Height: lay.Height, Title: svg.TitleLabel(db.Title, measurer, size, 4000).Plain()}
	for i, c := range cnodes {
		v := views[i]
		n := types.GeoNode{Shape: geoShapes[v.kind], X: c.X, Y: c.Y, W: v.w, H: v.h, Label: v.label.Plain(), Group: c.IsGroup, Parent: c.Parent}
		if v.node != nil {
			n.ID = v.node.ID
		}
		if c.IsGroup {
			n.W, n.H = c.W, c.H
		}
		if v.kind == K_Described {
			var lines []string
			for _, l := range v.lines {
				lines = append(lines, l.Plain()...)
			}
			n.Sections = [][]string{lines}
		}
		g.Nodes = append(g.Nodes, n)
	}
	for i, ce := range cedges {
		e := types.GeoEdge{From: ce.Src, To: ce.Dst, Route: append([]float64{}, ce.Route...), Start: "none", End: "arrow_point", Stroke: "normal",
			Label: edgeLabels[i].Plain(), LabelX: ce.LabelX, LabelY: ce.LabelY}
		if drawn[i].Pattern == "dashed" {
			e.End, e.Stroke = "none", "dotted"
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

// Picks where a composite's title sits in its title bar so that edges entering from above do not cross it.
func titleX(c *layout.CNode, width float64, crossings map[float64][]float64) float64 {
	xs, ok := crossings[js.Round(c.Y-c.H/2)]
	if !ok || width == 0 {
		return c.X
	}
	left := c.X - c.W/2
	half := width / 2
	best := c.X
	bestGap := -1.0
	for _, x := range [...]float64{c.X, left + GROUP_PAD + half, left + c.W - GROUP_PAD - half} {
		gap := math.Inf(1)
		for _, cx := range xs {
			if cx > left && cx < left+c.W {
				gap = math.Min(gap, math.Abs(cx-x)-half)
			}
		}
		if gap >= 6 {
			return x
		}
		if gap > bestGap+0.5 {
			bestGap = gap
			best = x
		}
	}
	return best
}
