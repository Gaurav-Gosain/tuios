// Ported from pele src/diagrams/ishikawa/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package ishikawa

import (
	"math"
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

// SLANT is how far a bone moves toward the tail for each unit it rises from the spine.
const (
	SLANT     = 0.5
	FIRST_ROW = 14
	ROW_GAP   = 6
	BONE_END  = 8
	TWIG_STEP = 22
	STUB      = 18
	TEXT_GAP  = 5
	PAIR_GAP  = 26
	HEAD_GAP  = 34
	TAIL      = 28
	BOX_PAD_X = 10
	BOX_PAD_Y = 5
)

var LENGTH = js.Hypot(SLANT, 1)

// Bone is a cause below a category. Bones at even depths run level, from the slanted bone above them
// toward the tail; bones at odd depths slant away from the spine, from the level bone above them.
type Bone struct {
	label  text.Label
	depth  int
	parent int
	// Its place among its siblings, counted from the spine outward.
	slot int
	kids int
	// How far its row is from the spine.
	rise float64
	// Where it meets its parent, measured level from the category's bone: 0 on it, negative toward the tail.
	at float64
	// Where its text ends.
	textEnd float64
}

type Category struct {
	node  *IshikawaNode
	label text.Label
	boxW  float64
	boxH  float64
	bones []Bone
	// The length of its bone, as distance from the spine.
	rise float64
	// How far its causes reach toward the tail, measured level from its bone.
	reach float64
}

type Frame struct {
	node   *IshikawaNode
	depth  int
	parent int
	slot   int
	// Set when the bone was made and only its row is still to come.
	bone int
}

// arrange gives every cause of a category a row of its own, so that no two texts can meet, and measures
// how far the whole of it reaches. A level bone takes the row nearest the spine and its twigs the
// rows beyond; a slanted twig takes the row beyond everything that hangs off it. The outline may
// be nested as deep as its indentation goes, so the walk keeps a stack of its own.
func arrange(node *IshikawaNode, up bool, measurer text.TextMeasurer, size, small float64) Category {
	label := text.LayoutLabel(node.text, false, measurer, size, 180, 0)
	lineHeight := js.Round(float64(small * 1.5))
	var bones []Bone
	var rows []int
	var stack []Frame
	// Above the spine the first cause goes farthest out, so that the causes read from the top down.
	push := func(children []*IshikawaNode, depth, parent int) {
		n := len(children)
		for slot := n - 1; slot >= 0; slot-- {
			child := children[slot]
			if up {
				child = children[n-1-slot]
			}
			stack = append(stack, Frame{node: child, depth: depth, parent: parent, slot: slot, bone: -1})
		}
	}
	push(node.children, 2, -1)
	for len(stack) > 0 {
		frame := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if frame.bone != -1 {
			rows = append(rows, frame.bone)
			continue
		}
		index := len(bones)
		bones = append(bones, Bone{
			label:  text.LayoutLabel(frame.node.text, false, measurer, small, 200, 0),
			depth:  frame.depth,
			parent: frame.parent,
			slot:   frame.slot,
			kids:   len(frame.node.children),
		})
		if frame.depth%2 == 0 {
			rows = append(rows, index)
		} else {
			frame.bone = index
			stack = append(stack, frame)
		}
		push(frame.node.children, frame.depth+1, index)
	}

	rise := float64(FIRST_ROW)
	for _, index := range rows {
		height := math.Max(bones[index].label.Height, lineHeight) + ROW_GAP
		bones[index].rise = rise + height/2
		rise += height
	}
	boxW := label.Width + 2*BOX_PAD_X
	reach := boxW / 2
	for i := range bones {
		bone := &bones[i]
		if bone.depth%2 == 0 {
			if bone.parent != -1 {
				bone.at = bones[bone.parent].at
			}
			step := float64(STUB)
			if bone.kids > 0 {
				step = float64(float64(bone.kids+1) * TWIG_STEP)
			}
			bone.textEnd = bone.at - step - TEXT_GAP
		} else {
			// Twigs stand along their level bone, the one nearest the spine nearest the tail.
			parent := &bones[bone.parent]
			bone.at = parent.at - float64(float64(parent.kids-bone.slot)*TWIG_STEP)
			bone.textEnd = bone.at - TEXT_GAP
		}
		reach = math.Max(reach, bone.label.Width-bone.textEnd)
	}
	return Category{node: node, label: label, boxW: boxW, boxH: label.Height + 2*BOX_PAD_Y, bones: bones, rise: rise + BONE_END, reach: reach}
}

func renderIshikawa(model *IshikawaDb, config types.Config, options types.RenderOptions) types.Rendered {
	return common.TurnToFit(options, func(down bool) types.Rendered { return draw(model, config, options, down) })
}

func draw(model *IshikawaDb, _ types.Config, options types.RenderOptions, down bool) types.Rendered {
	size := types.Or(options.FontSize, 16)
	small := js.Round(float64(size * 0.875))
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)
	if down {
		return verticalCauses(model, options)
	}
	root := model.root
	title := text.LayoutLabel(model.title, false, measurer, size, 4000, text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 12
	}

	var body strings.Builder
	minX, maxX, minY, maxY := 0.0, 0.0, 0.0, 0.0
	if root != nil {
		head := text.LayoutLabel(root.text, false, measurer, size, 200, text.Bold)
		headH := math.Max(head.Height+24, 48)
		headW := head.Width + 24 + headH/2
		categories := make([]Category, len(root.children))
		for i, node := range root.children {
			categories[i] = arrange(node, i%2 == 0, measurer, size, small)
		}
		// Even a category without causes has a bone long enough to carry its name clear of the head.
		for i := range categories {
			categories[i].rise = math.Max(categories[i].rise, headH/2+14)
		}

		// Categories meet the spine in pairs, one above and one below, each pair as far toward the
		// tail from the one before as the wider of the two needs.
		var xs []float64
		x := float64(-HEAD_GAP)
		for i := 0; i < len(categories); i += 2 {
			if i > 0 {
				room := 0.0
				for side := 0; side < 2; side++ {
					if i-2+side >= len(categories) {
						continue
					}
					before := &categories[i-2+side]
					next := 0.0
					if i+side < len(categories) {
						next = categories[i+side].boxW / 2
					}
					room = math.Max(room, before.reach+next)
				}
				x -= room + PAIR_GAP
			}
			xs = append(xs, x, x)
		}
		tail := float64(-HEAD_GAP)
		if len(categories) > 0 {
			tail = x - TAIL
		}

		maxX = headW
		minX = tail
		minY = -headH / 2
		maxY = headH / 2
		var groups strings.Builder
		for i := range categories {
			category := &categories[i]
			sign := 1.0
			if i%2 == 0 {
				sign = -1
			}
			ax := xs[i]
			px := func(at, rise float64) float64 { return ax + at - float64(rise*SLANT) }
			endX := px(0, category.rise)
			endY := float64(sign * category.rise)
			// The bone stops short of the spine to leave its arrowhead a clean point.
			trim := float64(svg.ARROW - 1)
			var d strings.Builder
			svg.WriteStrings(&d, "M", svg.Num(ax-float64((SLANT/LENGTH)*trim)), ",", svg.Num((sign*trim)/LENGTH), "L", svg.Num(endX), ",", svg.Num(endY))
			var texts strings.Builder
			for _, bone := range category.bones {
				y := float64(sign * bone.rise)
				if bone.depth%2 == 0 {
					svg.WriteStrings(&d, "M", svg.Num(px(bone.at, bone.rise)), ",", svg.Num(y), "H", svg.Num(px(bone.textEnd+TEXT_GAP, bone.rise)))
				} else {
					from := category.bones[bone.parent].rise
					svg.WriteStrings(&d, "M", svg.Num(px(bone.at, from)), ",", svg.Num(float64(sign*from)), "L", svg.Num(px(bone.at, bone.rise)), ",", svg.Num(y))
				}
				right := px(bone.textEnd, bone.rise)
				attrs := ""
				if bone.depth > 2 {
					attrs = ` fill="var(--_m)"`
				}
				texts.WriteString(svg.LabelSvg(bone.label, right-bone.label.Width/2, y, attrs, nil))
				minX = math.Min(minX, right-bone.label.Width)
			}
			boxY := endY + float64(sign*(category.boxH/2))
			minX = math.Min(minX, endX-category.boxW/2)
			maxX = math.Max(maxX, endX+category.boxW/2)
			minY = math.Min(minY, boxY-category.boxH/2)
			maxY = math.Max(maxY, boxY+category.boxH/2)
			svg.WriteStrings(&groups, `<g class="pele-ishikawa-category" data-id="`, svg.EscText(category.node.text), `">`,
				`<path class="pele-ishikawa-bones" d="`, d.String(), `" fill="none" stroke="var(--_l)"/>`)
			svg.WriteMarker(&groups, "arrow_point", ax, 0, SLANT/LENGTH, -sign/LENGTH, "var(--_l)")
			svg.WriteStrings(&groups, `<rect x="`, svg.Num(endX-category.boxW/2), `" y="`, svg.Num(boxY-category.boxH/2), `" width="`, svg.Num(category.boxW),
				`" height="`, svg.Num(category.boxH), `" fill="var(--_s)" stroke="var(--_b)"`, svg.ROUNDED, `/>`,

				svg.LabelSvg(category.label, endX, boxY, ` class="pele-label"`, nil))
			if texts.Len() > 0 {
				svg.WriteStrings(&groups, `<g class="pele-ishikawa-causes" font-size="`, js.Num(small), `">`, texts.String(), `</g>`)
			}
			groups.WriteString(`</g>`)
		}

		r := headH / 2
		svg.WriteStrings(&body, `<path class="pele-ishikawa-spine" d="M`, svg.Num(tail), `,0H`, svg.Num(-(svg.ARROW - 1)), `" fill="none" stroke="var(--_l)"/>`)
		svg.WriteMarker(&body, "arrow_point", 0, 0, 1, 0, "var(--_l)")
		body.WriteString(groups.String())
		svg.WriteStrings(&body, `<g class="pele-ishikawa-head" data-id="`, svg.EscText(root.text), `">`,
			`<path d="M0,`, svg.Num(-r), "H", svg.Num(headW-r), "A", svg.Num(r), ",", svg.Num(r), " 0 0 1 ", svg.Num(headW-r), ",", svg.Num(r),
			`H0Z" fill="var(--_s)" stroke="var(--_b)" stroke-linejoin="round"/>`,
			svg.LabelSvg(head, 12+head.Width/2, 0, ` class="pele-label" font-weight="var(--_hw)"`, nil),
			`</g>`)
	}

	inner := math.Max(maxX-minX, title.Width)
	width := math.Ceil(inner + float64(2*pad))
	height := math.Ceil(maxY - minY + titleHeight + float64(2*pad))
	ox := pad + (inner-(maxX-minX))/2 - minX
	oy := pad + titleHeight - minY
	var content strings.Builder
	content.WriteString(svg.LabelSvg(title, width/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil))
	if body.Len() > 0 {
		svg.WriteStrings(&content, `<g transform="translate(`, svg.Num(ox), ",", svg.Num(oy), `)">`, body.String(), `</g>`)
	}
	rendered := types.Rendered{
		SVG:    svg.SvgDocument("ishikawa", width, height, size, options, svg.Accessible{}, content.String()),
		Width:  width,
		Height: height,
		Links:  []types.LinkInfo{},
	}
	if options.Geometry != nil {
		rendered.Geometry = &types.Geometry{Width: width, Height: height, Ishikawa: geometry(model, title, measurer)}
	}
	return rendered
}

type entry struct {
	node       *IshikawaNode
	depth      int
	parent     int
	x, y, w, h float64
	label      text.Label
}

func verticalCauses(model *IshikawaDb, options types.RenderOptions) types.Rendered {
	size := types.Or(options.FontSize, 16)
	small := js.Round(float64(size * 0.875))
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)
	room := math.Max(140, types.Or(options.MaxWidth, 320)-float64(2*pad))
	title := text.LayoutLabel(model.title, false, measurer, size, common.TitleRoom(options), text.Bold)
	var order []entry
	type pendingEntry struct {
		node          *IshikawaNode
		depth, parent int
	}
	var pending []pendingEntry
	if model.root != nil {
		pending = append(pending, pendingEntry{model.root, 0, -1})
	}
	depth := 0
	for len(pending) > 0 {
		e := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		index := len(order)
		order = append(order, entry{node: e.node, depth: e.depth, parent: e.parent})
		depth = max(depth, e.depth)
		for i := len(e.node.children) - 1; i >= 0; i-- {
			pending = append(pending, pendingEntry{e.node.children[i], e.depth + 1, index})
		}
	}
	indent := math.Min(20, math.Max(2, (room-120)/math.Max(1, float64(depth))))
	y := pad
	if title.Height != 0 {
		y = pad + (title.Height + 12)
	}
	width := title.Width
	var nodes, edges strings.Builder
	for k := range order {
		e := &order[k]
		e.x = pad + float64(float64(e.depth)*indent)
		labelRoom := math.Max(60, room-float64(float64(e.depth)*indent)-20)
		fontSize, style := small, 0
		if e.depth < 2 {
			fontSize, style = size, text.Bold
		}
		e.label = common.BreakLabel(text.LayoutLabel(e.node.text, false, measurer, fontSize, labelRoom, style), measurer, labelRoom)
		e.w = e.label.Width + 20
		e.h = e.label.Height + 12
		e.y = y
		y += e.h + 12
		width = math.Max(width, e.x-pad+e.w)
		framed := e.depth < 2
		class := "pele-ishikawa-cause"
		if e.depth == 0 {
			class = "pele-ishikawa-head"
		} else if e.depth == 1 {
			class = "pele-ishikawa-category"
		}
		svg.WriteStrings(&nodes, `<g class="pele-node `, class, `" data-id="`, svg.EscText(e.node.text), `">`)
		if framed {
			rx := "4"
			if e.depth == 0 {
				rx = "12"
			}
			svg.WriteStrings(&nodes, `<rect x="`, svg.Num(e.x), `" y="`, svg.Num(e.y), `" width="`, svg.Num(e.w), `" height="`, svg.Num(e.h), `" rx="`, rx, `" fill="var(--_s)" stroke="var(--_b)"/>`)
		}
		attrs := ` class="pele-label"`
		if e.depth < 2 {
			attrs += ` font-weight="var(--_hw)"`
		}
		if e.depth > 1 {
			attrs += ` font-size="` + js.Num(small) + `"`
		}
		svg.WriteStrings(&nodes, svg.LabelSvg(e.label, e.x+e.w/2, e.y+e.h/2, attrs, nil), `</g>`)
		if e.parent >= 0 {
			parent := &order[e.parent]
			gx := parent.x + indent/2
			svg.WriteStrings(&edges, `<path class="pele-edge" d="M`, svg.Num(gx), ",", svg.Num(parent.y+parent.h), "V", svg.Num(e.y+e.h/2), "H", svg.Num(e.x), `" fill="none" stroke="var(--_b)"/>`)
		}
	}
	totalWidth := math.Ceil(width + float64(2*pad))
	height := math.Ceil(math.Max(pad+title.Height, y-12) + pad)
	content := svg.LabelSvg(title, totalWidth/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil) + edges.String() + nodes.String()
	rendered := types.Rendered{
		SVG:    svg.SvgDocument("ishikawa", totalWidth, height, size, options, svg.Accessible{}, content),
		Width:  totalWidth,
		Height: height,
		Links:  []types.LinkInfo{},
	}
	if options.Geometry != nil {
		rendered.Geometry = &types.Geometry{Width: totalWidth, Height: height, Ishikawa: geometry(model, title, measurer)}
	}
	return rendered
}

// geometry is the outline as lists, for a drawing in text. Not part of pele.
func geometry(model *IshikawaDb, title text.Label, measurer text.TextMeasurer) *types.GeoIshikawa {
	geo := &types.GeoIshikawa{Title: title.Plain()}
	plain := func(node *IshikawaNode) string {
		return strings.Join(text.LayoutLabel(node.text, false, measurer, 16, math.Inf(1), 0).Plain(), " ")
	}
	if model.root == nil {
		return geo
	}
	geo.Effect = plain(model.root)
	for _, category := range model.root.children {
		c := types.GeoCategory{Label: plain(category)}
		type item struct {
			node  *IshikawaNode
			depth int
		}
		var stack []item
		for i := len(category.children) - 1; i >= 0; i-- {
			stack = append(stack, item{category.children[i], 1})
		}
		for len(stack) > 0 {
			it := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			c.Causes = append(c.Causes, types.GeoCause{Label: plain(it.node), Depth: it.depth})
			for i := len(it.node.children) - 1; i >= 0; i-- {
				stack = append(stack, item{it.node.children[i], it.depth + 1})
			}
		}
		geo.Categories = append(geo.Categories, c)
	}
	return geo
}
