// Ported from pele src/diagrams/railroad/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package railroad

import (
	"math"
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

const (
	KindTerminal = iota
	KindNonTerminal
	KindSpecial
	KindSequence
	KindChoice
	KindBypass
	KindLoop
	KindFolded
)

// Box is a laid-out element. The track enters at the left edge and leaves at the right edge on one
// baseline, with `up` above it and `down` below it.
type Box struct {
	kind  int
	width float64
	up    float64
	down  float64
	kids  []*Box
	// A leaf's text, or a loop's count.
	text string
	// The name a non-terminal refers to.
	id string
	// How far a bypass rises above the baseline, or a loop drops below it.
	reach float64
}

type Metrics struct {
	measurer text.TextMeasurer
	size     float64
	padding  float64
	height   float64
	// Space between elements in a sequence, and between stacked branches.
	gap float64
	sep float64
	arc float64
}

const (
	LEAD       = 12
	HEAD_GAP   = 6
	RULE_GAP   = 18
	COUNT_SIZE = 12
	ARROW      = 7
)

func option(config types.Config, key string, fallback, max float64) float64 {
	section, _ := config["railroad"].(map[string]any)
	if value, ok := section[key].(float64); ok && value >= 0 {
		return math.Min(value, max)
	}
	return fallback
}

// shown: text is literal here: a terminal such as "<br>" or "*" is itself, not markup. Mermaid's `#35;`
// codes still stand for characters, and escapes that would be invisible are written out.
func shown(t string) string {
	s := t
	if strings.Contains(t, "¶ß") {
		s = text.DecodeEntities(strings.ReplaceAll(t, "&", "&amp;"))
	}
	if !strings.ContainsAny(s, "\n\r\t") {
		return s
	}
	return strings.NewReplacer("\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(s)
}

func reverseBoxes(list []*Box, from int) {
	for i, j := from, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
}

func reverseFloats(list []float64, from int) {
	for i, j := from, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
}

func even(value float64) float64 {
	return math.Ceil(value/2) * 2
}

func leaf(kind int, t, id string, style int, m *Metrics) *Box {
	half := m.height / 2
	width := math.Max(even(m.measurer.Width(t, m.size, style)+float64(2*m.padding)), m.height)
	return &Box{kind: kind, width: width, up: half, down: half, text: t, id: id}
}

func count(min, max float64) string {
	if math.IsInf(max, 1) && min <= 1 {
		return ""
	}
	str := func(value float64) string {
		if !math.IsNaN(value) && !math.IsInf(value, 0) {
			return js.Num(value)
		}
		return "∞"
	}
	if min == max {
		return str(min) + "×"
	}
	if math.IsInf(max, 1) {
		return "≥" + str(min) + "×"
	}
	if min == 0 {
		return "≤" + str(max) + "×"
	}
	return str(min) + "–" + str(max) + "×"
}

// below is where the next branch of a choice sits below the baseline, given the one above it.
func below(offset float64, above, branch *Box, m *Metrics) float64 {
	return math.Max(offset+above.down+m.sep+branch.up, float64(2*m.arc))
}

func bypass(kid *Box, m *Metrics) *Box {
	reach := math.Max(kid.up+m.sep, float64(2*m.arc))
	return &Box{kind: KindBypass, width: kid.width + float64(4*m.arc), up: reach, down: kid.down, kids: []*Box{kid}, reach: reach}
}

// allSpaces is /^ +$/.
func allSpaces(s string) bool {
	return s != "" && strings.Trim(s, " ") == ""
}

func measure(root *RailroadNode, m *Metrics, room float64) *Box {
	var budgets map[*RailroadNode]float64
	if !math.IsInf(room, 1) {
		budgets = map[*RailroadNode]float64{}
		type item struct {
			node  *RailroadNode
			width float64
		}
		pending := []item{{root, room}}
		for i := 0; i < len(pending); i++ {
			node, width := pending[i].node, pending[i].width
			budgets[node] = width
			// A sequence that folds puts its rows inside rails, so its parts are sized for those rows.
			around := 0.0
			switch node.typ {
			case "choice", "optional":
				around = float64(4 * m.arc)
			case "repetition":
				if node.min == 0 {
					around = float64(6 * m.arc)
				} else {
					around = float64(2 * m.arc)
				}
			case "sequence":
				around = float64(6 * m.arc)
			}
			for _, child := range modelParts(node) {
				pending = append(pending, item{child, math.Max(40, width-around)})
			}
		}
	}
	return fold(root, modelParts, func(node *RailroadNode, kids []*Box) *Box {
		room := math.Inf(1)
		if budgets != nil {
			if b, ok := budgets[node]; ok {
				room = b
			}
		}
		switch node.typ {
		case "terminal":
			t := shown(node.value)
			if allSpaces(t) {
				t = strings.Repeat("␣", len(t))
			}
			return leaf(KindTerminal, t, "", text.Mono, m)
		case "nonterminal":
			return leaf(KindNonTerminal, shown(node.name), node.name, 0, m)
		case "special":
			return leaf(KindSpecial, "? "+shown(node.text)+" ?", "", text.Italic, m)
		case "sequence":
			width := float64(float64(max(0, len(kids)-1)) * m.gap)
			up, down := 0.0, 0.0
			for _, kid := range kids {
				width += kid.width
				up = math.Max(up, kid.up)
				down = math.Max(down, kid.down)
			}
			seq := &Box{kind: KindSequence, width: width, up: up, down: down, kids: kids}
			if width <= room || len(kids) < 2 {
				return seq
			}
			var rows []*Box
			var row []*Box
			rowW := 0.0
			flush := func() {
				up, down := 0.0, 0.0
				for _, kid := range row {
					up = math.Max(up, kid.up)
					down = math.Max(down, kid.down)
				}
				rows = append(rows, &Box{kind: KindSequence, width: rowW, up: up, down: down, kids: row})
				row = nil
				rowW = 0
			}
			for _, kid := range kids {
				if len(row) > 0 && rowW+m.gap+kid.width > math.Max(40, room-float64(6*m.arc)) {
					flush()
				}
				if len(row) > 0 {
					rowW += m.gap
				}
				rowW += kid.width
				row = append(row, kid)
			}
			flush()
			if len(rows) < 2 {
				return seq
			}
			foldedWidth, offset := 0.0, 0.0
			for i := range rows {
				foldedWidth = math.Max(foldedWidth, rows[i].width)
				if i > 0 {
					offset += rows[i-1].down + math.Max(m.sep, float64(2*m.arc)) + rows[i].up
				}
			}
			return &Box{kind: KindFolded, width: foldedWidth + float64(6*m.arc), up: rows[0].up, down: offset + rows[len(rows)-1].down, kids: rows}
		case "choice":
			inner, offset := 0.0, 0.0
			for i := range kids {
				inner = math.Max(inner, kids[i].width)
				if i > 0 {
					offset = below(offset, kids[i-1], kids[i], m)
				}
			}
			b := &Box{kind: KindChoice, width: inner + float64(4*m.arc), kids: kids}
			if len(kids) > 0 {
				b.up = kids[0].up
				b.down = offset + kids[len(kids)-1].down
			}
			return b
		case "optional":
			return bypass(kids[0], m)
		}
		// repetition
		min, max := node.min, node.max
		kid := kids[0]
		if min == 1 && max == 1 {
			return kid
		}
		t := count(min, max)
		reach := math.Max(kid.down+m.sep, float64(2*m.arc))
		label := 0.0
		if t != "" {
			label = even(m.measurer.Width(t, COUNT_SIZE, 0))
		}
		down := reach + COUNT_SIZE + 6
		if t == "" {
			down = reach + ARROW/2.0 + 1
		}
		loop := &Box{kind: KindLoop, width: math.Max(kid.width, label) + float64(2*m.arc), up: kid.up, down: down, kids: []*Box{kid}, text: t, reach: reach}
		if min == 0 {
			return bypass(loop, m)
		}
		return loop
	})
}

func renderRailroad(model *RailroadDb, config types.Config, options types.RenderOptions) types.Rendered {
	return common.Tighten(options, func(tight bool) types.Rendered { return draw(model, config, options, tight) })
}

func draw(model *RailroadDb, config types.Config, options types.RenderOptions, tight bool) types.Rendered {
	base := types.Or(options.FontSize, 16)
	fontSize := option(config, "fontSize", 0, 200)
	size := fontSize
	if size == 0 {
		size = js.Round(float64(base * 0.875))
	}
	headSize := fontSize
	if headSize == 0 {
		headSize = base
	}
	padding := option(config, "padding", -1, 200)
	lineHeight := js.Round(float64(size * 1.5))
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	m := &Metrics{measurer: measurer, size: size, padding: padding, sep: option(config, "verticalSeparation", 10, 500)}
	if padding < 0 {
		m.padding = 12
		m.height = even(lineHeight + 9)
	} else {
		m.height = even(lineHeight + float64(2*padding))
	}
	m.gap = option(config, "horizontalSeparation", 16, 500)
	m.arc = option(config, "arcRadius", 10, 200)
	if tight {
		m.gap = math.Min(m.gap, 12)
		m.arc = math.Min(m.arc, 6)
	}
	section, _ := config["railroad"].(map[string]any)
	radius := option(config, "markerRadius", 4, 50)
	if section["showMarkers"] == false {
		radius = 0
	}
	strokeWidth := option(config, "strokeWidth", 0, 20)
	stroke := ""
	if strokeWidth > 0 {
		stroke = ` stroke-width="` + svg.Num(strokeWidth) + `"`
	}
	pad := types.Or(options.Padding, 8)
	arc := m.arc

	A := svg.Num(arc)
	turn := func(sweep, dx, dy string) string {
		return "a" + A + "," + A + " 0 0 " + sweep + " " + dx + A + "," + dy + A
	}
	eastToSouth := turn("1", "", "")
	southToEast := turn("0", "", "")
	eastToNorth := turn("0", "", "-")
	northToEast := turn("1", "", "-")
	loopDown := turn("0", "-", "")
	loopUp := turn("0", "-", "-")

	titleRoom := 4000.0
	if tight {
		titleRoom = common.TitleRoom(options)
	}
	title := text.LayoutLabel(model.title, false, measurer, base, titleRoom, text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 16
	}
	headHeight := js.Round(float64(headSize * 1.5))
	textShift := float64(size * 0.35)
	boxTop := m.height / 2

	var track, arrows, nodes strings.Builder
	line := func(x1, y, x2 float64) {
		if x2 > x1 {
			svg.WriteStrings(&track, "M", svg.Num(x1), ",", svg.Num(y), "H", svg.Num(x2))
		}
	}

	place := func(root *Box, left, baseline float64) {
		boxes := []*Box{root}
		at := []float64{left, baseline}
		for len(boxes) > 0 {
			box := boxes[len(boxes)-1]
			boxes = boxes[:len(boxes)-1]
			y := at[len(at)-1]
			x := at[len(at)-2]
			at = at[:len(at)-2]
			kids := box.kids
			right := x + box.width
			switch box.kind {
			case KindSequence:
				kx := right
				for i := len(kids) - 1; i >= 0; i-- {
					kx -= kids[i].width
					boxes = append(boxes, kids[i])
					at = append(at, kx, y)
					if i > 0 {
						line(kx-m.gap, y, kx)
					}
					kx -= m.gap
				}
			case KindFolded:
				start := x + float64(2*arc)
				from := len(boxes)
				offset := 0.0
				line(x, y, start)
				gapRow := math.Max(m.sep, float64(2*arc))
				for i, kid := range kids {
					ky := y + offset
					boxes = append(boxes, kid)
					at = append(at, ky, start)
					if i+1 < len(kids) {
						next := kids[i+1]
						nextOffset := offset + kid.down + gapRow + next.up
						mid := y + offset + kid.down + gapRow/2
						svg.WriteStrings(&track, "M", svg.Num(start+kid.width), ",", svg.Num(ky), "H", svg.Num(right-float64(2*arc)), "V", svg.Num(mid),
							"H", svg.Num(x+arc), "V", svg.Num(y+nextOffset), "H", svg.Num(start))
						svg.WriteStrings(&arrows, "M", svg.Num(start-ARROW), ",", svg.Num(y+nextOffset-ARROW/2.0), "l7,3.5l-7,3.5z")
						offset = nextOffset
					} else {
						svg.WriteStrings(&track, "M", svg.Num(start+kid.width), ",", svg.Num(ky), "H", svg.Num(right-arc), "V", svg.Num(y), "H", svg.Num(right))
					}
				}
				reverseBoxes(boxes, from)
				reverseFloats(at, 2*from)
			case KindChoice:
				inner := box.width - float64(4*arc)
				railIn := x + arc
				railOut := right - arc
				from := len(boxes)
				offset := 0.0
				for i, kid := range kids {
					if i > 0 {
						offset = below(offset, kids[i-1], kid, m)
					}
					ky := y + offset
					kx := x + float64(2*arc) + (inner-kid.width)/2
					last := i == len(kids)-1
					if i == 0 {
						line(x, y, kx)
						line(kx+kid.width, y, right)
					} else {
						if last {
							svg.WriteStrings(&track, "M", svg.Num(x), ",", svg.Num(y), eastToSouth, "V", svg.Num(ky-arc))
						} else {
							svg.WriteStrings(&track, "M", svg.Num(railIn), ",", svg.Num(ky-arc))
						}
						track.WriteString(southToEast)
						if kx > railIn+arc {
							svg.WriteStrings(&track, "H", svg.Num(kx))
						}
						svg.WriteStrings(&track, "M", svg.Num(kx+kid.width), ",", svg.Num(ky))
						if kx > railIn+arc {
							svg.WriteStrings(&track, "H", svg.Num(railOut-arc))
						}
						track.WriteString(eastToNorth)
						if last {
							svg.WriteStrings(&track, "V", svg.Num(y+arc), northToEast)
						}
					}
					boxes = append(boxes, kid)
					at = append(at, ky, kx)
				}
				// Reversed, so that the branches come off the stack top to bottom.
				reverseBoxes(boxes, from)
				reverseFloats(at, 2*from)
			case KindBypass:
				kid := kids[0]
				kx := x + float64(2*arc)
				line(x, y, kx)
				line(kx+kid.width, y, right)
				svg.WriteStrings(&track, "M", svg.Num(x), ",", svg.Num(y), eastToNorth, "V", svg.Num(y-box.reach+arc), northToEast, "H", svg.Num(right-float64(2*arc)),
					eastToSouth, "V", svg.Num(y-arc), southToEast)
				boxes = append(boxes, kid)
				at = append(at, kx, y)
			case KindLoop:
				kid := kids[0]
				kx := x + (box.width-kid.width)/2
				loopY := y + box.reach
				middle := x + box.width/2
				line(x, y, kx)
				line(kx+kid.width, y, right)
				svg.WriteStrings(&track, "M", svg.Num(x+arc), ",", svg.Num(y), loopDown, "V", svg.Num(loopY-arc), southToEast, "H", svg.Num(right-arc),
					eastToNorth, "V", svg.Num(y+arc), loopUp)
				svg.WriteStrings(&arrows, "M", svg.Num(middle-ARROW/2.0), ",", svg.Num(loopY), "l7,-3.5v7z")
				if box.text != "" {
					svg.WriteStrings(&nodes, `<text class="pele-edge-label pele-count" x="`, svg.Num(middle), `" y="`, svg.Num(loopY+COUNT_SIZE+3), `" font-size="12" fill="var(--_m)">`, svg.Esc(box.text), `</text>`)
				}
				boxes = append(boxes, kid)
				at = append(at, kx, y)
			default:
				t := box.text
				space := ""
				if strings.HasPrefix(t, " ") || strings.HasSuffix(t, " ") || strings.Contains(t, "  ") {
					space = ` xml:space="preserve"`
				}
				shape := `<rect x="` + svg.Num(x) + `" y="` + svg.Num(y-boxTop) + `" width="` + svg.Num(box.width) + `" height="` + svg.Num(m.height) + `"`
				label := `<text x="` + svg.Num(x+box.width/2) + `" y="` + svg.Num(y+textShift) + `"` + space
				switch box.kind {
				case KindTerminal:
					svg.WriteStrings(&nodes, `<g class="pele-node pele-terminal">`, shape, ` rx="`, svg.Num(boxTop), `" fill="var(--_s)" stroke="var(--_b)"`, stroke, `/>`,
						label, ` font-family="`, svg.FONT_MONO, `">`, svg.Esc(t), `</text></g>`)
				case KindNonTerminal:
					svg.WriteStrings(&nodes, `<g class="pele-node pele-nonterminal" data-id="`, svg.EscText(box.id), `">`, shape, ` fill="var(--_bg)" stroke="var(--_b)"`, stroke, svg.ROUNDED, `/>`,
						label, `>`, svg.Esc(t), `</text></g>`)
				default:
					svg.WriteStrings(&nodes, `<g class="pele-node pele-special">`, shape, ` fill="var(--_bg)" stroke="var(--_b)" stroke-dasharray="4 3"`, stroke, svg.ROUNDED, `/>`,

						label, ` font-style="italic"><tspan fill="var(--_m)">? </tspan>`, svg.Esc(t[2:len(t)-2]), `<tspan fill="var(--_m)"> ?</tspan></text></g>`)
				}
			}
		}
	}

	var rules strings.Builder
	width := title.Width
	top := pad + titleHeight
	bottom := pad + title.Height
	room := math.Inf(1)
	if tight {
		room = math.Max(80, types.Or(options.MaxWidth, 320)-float64(2*pad)-float64(4*radius)-2*LEAD-1)
	}
	headText := svg.Num(headSize)
	var geo *types.GeoRailroad
	if options.Geometry != nil {
		geo = &types.GeoRailroad{Title: title.Plain()}
	}
	for _, rule := range model.rules {
		box := measure(rule.definition, m, room)
		name := shown(rule.name)
		baseline := top + headHeight + HEAD_GAP + box.up
		start := pad + float64(2*radius)
		left := start + LEAD
		end := left + box.width + LEAD
		track.Reset()
		arrows.Reset()
		nodes.Reset()
		line(start, baseline, left)
		line(left+box.width, baseline, end)
		place(box, left, baseline)
		svg.WriteStrings(&rules, `<g class="pele-rule" data-id="`, svg.EscText(rule.name), `">`,
			`<text class="pele-rule-name" x="`, svg.Num(pad), `" y="`, svg.Num(top+headHeight/2+float64(headSize*0.35)), `" font-size="`, headText,
			`" font-weight="var(--_hw)" text-anchor="start">`, svg.Esc(name), `</text>`,
			`<path class="pele-edge pele-track" d="`, track.String(), `" fill="none" stroke="var(--_l)"`, stroke, `/>`)
		if arrows.Len() > 0 {
			svg.WriteStrings(&rules, `<path class="pele-marker" d="`, arrows.String(), `" fill="var(--_l)"/>`)
		}
		if radius > 0 {
			svg.WriteStrings(&rules, `<circle class="pele-marker pele-start" cx="`, svg.Num(pad+radius), `" cy="`, svg.Num(baseline), `" r="`, svg.Num(radius), `" fill="var(--_l)"/>`,
				`<circle class="pele-marker pele-end" cx="`, svg.Num(end+radius), `" cy="`, svg.Num(baseline), `" r="`, svg.Num(radius), `" fill="var(--_l)"/>`)
		}
		svg.WriteStrings(&rules, nodes.String(), `</g>`)
		width = math.Max(width, math.Max(end+float64(2*radius)-pad, measurer.Width(name, headSize, text.Bold)))
		bottom = baseline + box.down
		top = bottom + m.sep + RULE_GAP
		if geo != nil {
			geo.Rules = append(geo.Rules, types.GeoRule{Name: name, Root: geoNode(rule.definition)})
		}
	}

	totalWidth := math.Ceil(width + float64(2*pad))
	totalHeight := math.Ceil(bottom + pad)
	var content strings.Builder
	content.WriteString(svg.LabelSvg(title, totalWidth/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil))
	if rules.Len() > 0 {
		svg.WriteStrings(&content, `<g class="pele-rules" transform="translate(0.5,0.5)" font-size="`, svg.Num(size), `" text-anchor="middle">`, rules.String(), `</g>`)
	}
	rendered := types.Rendered{
		SVG:    svg.SvgDocument("railroad", totalWidth, totalHeight, base, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr}, content.String()),
		Width:  totalWidth,
		Height: totalHeight,
		Links:  []types.LinkInfo{},
	}
	if geo != nil {
		rendered.Geometry = &types.Geometry{Width: totalWidth, Height: totalHeight, Railroad: geo}
	}
	return rendered
}

// geoNode is a rule's tree for a drawing in text, with its texts as the SVG shows them. Not part of pele.
func geoNode(root *RailroadNode) *types.GeoRailNode {
	return fold(root, modelParts, func(node *RailroadNode, kids []*types.GeoRailNode) *types.GeoRailNode {
		out := &types.GeoRailNode{Kind: node.typ, Kids: kids, Min: node.min, Max: node.max}
		switch node.typ {
		case "terminal":
			out.Text = shown(node.value)
			if allSpaces(out.Text) {
				out.Text = strings.Repeat("␣", len(out.Text))
			}
		case "nonterminal":
			out.Text = shown(node.name)
		case "special":
			out.Text = shown(node.text)
		}
		return out
	})
}
