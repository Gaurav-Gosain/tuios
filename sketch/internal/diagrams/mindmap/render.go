// Ported from pele src/diagrams/mindmap/render.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.
//
// pele's closures over the layout arrays are methods of a struct here.

package mindmap

import (
	"math"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

// SHAPES is indexed by Mermaid's node type. A node without delimiters is text on a line, except the root.
var SHAPES = [...]string{"", "rounded", "rect", "circle", "cloud", "bang", "hex"}

const ROOT_SHAPE = "stadium"

const (
	GAP_X = 36
	GAP_Y = 8
	// Between two subtrees, of which at least one has children of its own.
	GROUP_GAP    = 18
	BRANCH_GAP_X = 52
	BRANCH_GAP_Y = 28
	TEXT_PAD     = 6
	PILL_PAD     = 12
	TINT         = 0.22
	ICON_GAP     = 6
	// Running down, each level is set in by this much, and links leave a node this far from its left edge.
	INDENT  = 28
	STEM    = 12
	BEND    = 8
	ROW_GAP = 10
)

// A label with each line starting at `left`, for an outline, where centered lines would look ragged.
func leftLabel(label text.Label, left, cy float64, icons types.IconResolver) string {
	if len(label.Lines) < 2 {
		return svg.LabelSvg(label, left+label.Width/2, cy, ` class="pele-label"`, icons)
	}
	out := ""
	for k := range label.Lines {
		width := label.Widths[k]
		line := text.Label{Lines: label.Lines[k : k+1], Widths: []float64{width}, Width: width, Height: label.LineHeight, Size: label.Size, LineHeight: label.LineHeight}
		out += svg.LabelSvg(line, left+width/2, cy+(float64(k)-float64(len(label.Lines)-1)/2)*label.LineHeight, ` class="pele-label"`, icons)
	}
	return out
}

func renderMindmap(model *MindmapDb, config types.Config, options types.RenderOptions) types.Rendered {
	return common.TurnToFit(options, func(down bool) types.Rendered { return draw(model, config, options, down) })
}

// The layout arrays that pele's `stack` closure works on.
type bands struct {
	extent  []float64
	rel     []float64
	own     []float64
	anchor  []float64
	stacked float64
}

// Stacks subtrees top to bottom and returns the height at which their parent's anchor belongs.
func (b *bands) stack(list []*MindmapNode, from, to int, gap float64) float64 {
	y := 0.0
	last := 0.0
	for k := from; k < to; k++ {
		c := list[k].id
		b.rel[c] = y
		grouped := len(list[k].children) > 0 || (k+1 < to && len(list[k+1].children) > 0)
		if grouped {
			last = math.Max(gap, GROUP_GAP)
		} else {
			last = gap
		}
		y += b.extent[c] + last
	}
	b.stacked = y - last
	a := list[from].id
	z := list[to-1].id
	return (b.rel[a] + b.own[a] + b.anchor[a] + b.rel[z] + b.own[z] + b.anchor[z]) / 2
}

// The branch being drawn, flushed into the output as one group.
type branch struct {
	out   string
	edges string
	body  string
	color string
}

func (g *branch) flush(section int) {
	if g.body == "" {
		return
	}
	g.out += `<g class="pele-branch pele-section-` + js.Int(section) + `">` +
		`<g class="pele-edges" fill="none" stroke="` + g.color + `" stroke-width="1.5">` + g.edges + `</g>` + g.body + `</g>`
	g.edges = ""
	g.body = ""
}

// `down` draws the map as an outline: the root at the top, and each level set in under its parent.
// It is far narrower than branches spreading to both sides, and as tall as it needs to be.
func draw(model *MindmapDb, config types.Config, options types.RenderOptions, down bool) types.Rendered {
	num := svg.Num
	size := types.Or(options.FontSize, 16)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, "")
	}
	pad := types.Or(options.Padding, 8)
	icons := options.Icons
	wrap := 200.0
	if conf, ok := config["mindmap"].(map[string]any); ok {
		if maxNodeWidth, ok := conf["maxNodeWidth"].(float64); ok && maxNodeWidth > 0 {
			wrap = math.Min(maxNodeWidth, 2000)
		}
	}
	glyph := size * 1.25

	nodes := model.nodes
	n := len(nodes)
	parent := make([]int32, n)
	depth := make([]int32, n)
	for i := 0; i < n; i++ {
		for _, child := range nodes[i].children {
			parent[child.id] = int32(i)
			depth[child.id] = depth[i] + 1
		}
	}
	// An outline has the width the host gives it, less what its level is set in.
	room := math.Inf(1)
	if down && options.MaxWidth != nil && *options.MaxWidth > 0 {
		room = *options.MaxWidth - 2*pad
	}
	labels := make([]text.Label, n)
	shapes := make([]string, n)
	w := make([]float64, n)
	h := make([]float64, n)
	dy := make([]float64, n)
	// Where links meet the node, measured down from its top: its middle.
	anchor := make([]float64, n)
	// Whether the host has the node's icon. One it does not have takes no room.
	drawn := make([]bool, n)

	for i := 0; i < n; i++ {
		node := nodes[i]
		drawn[i] = node.icon != "" && text.IconMarkup(measurer, node.icon) != ""
		iconRoom := 0.0
		if drawn[i] {
			iconRoom = glyph + ICON_GAP
		}
		fit := math.Min(wrap, math.Max(72, room-float64(depth[i])*INDENT-2*TEXT_PAD-iconRoom))
		labels[i] = text.LayoutLabel(node.descr, true, measurer, size, fit, 0)
		label := labels[i]
		tw := label.Width
		if drawn[i] {
			tw += glyph
			if label.Width > 0 {
				tw += ICON_GAP
			}
		}
		th := math.Max(label.Height, js.Round(size*1.5))
		shape := ""
		if node.typ >= 0 && node.typ < len(SHAPES) {
			shape = SHAPES[node.typ]
		}
		if shape == "" && i == 0 {
			shape = ROOT_SHAPE
		}
		// In an outline the root heads a column, and the trunk drops from under its left end. A circle
		// or a cloud would stand wide of that, so the root is a pill there unless it is a box already.
		if down && i == 0 && shape != "rect" && shape != "rounded" {
			shape = ROOT_SHAPE
		}
		shapes[i] = shape
		if shape == "" {
			// A node with no shape of its own is plain text when it ends a branch, and text in a
			// tinted pill when the branch goes on from it, which sets the levels apart.
			pill := len(node.children) > 0
			if pill {
				w[i] = tw + 2*PILL_PAD
				h[i] = th + 6
			} else {
				w[i] = tw + 2*TEXT_PAD
				h[i] = th
			}
			anchor[i] = h[i] / 2
		} else {
			s := svg.ShapeSizeOf(shape, tw, th)
			w[i] = s.W
			h[i] = s.H
			dy[i] = s.Dy
			anchor[i] = s.H / 2
		}
	}

	// Each subtree takes a band of height `extent`. `rel` is a subtree's offset among its siblings,
	// `own` the node's offset inside its band, and `kids` the offset of its children's bands.
	b := &bands{extent: make([]float64, n), rel: make([]float64, n), own: make([]float64, n), anchor: anchor}
	extent, rel, own := b.extent, b.rel, b.own
	kids := make([]float64, n)

	// Nodes are in depth-first order, so going backwards visits children before their parent.
	for i := n - 1; i > 0; i-- {
		children := nodes[i].children
		if len(children) == 0 {
			extent[i] = h[i]
			continue
		}
		top := b.stack(children, 0, len(children), GAP_Y) - anchor[i]
		low := math.Min(0, top)
		extent[i] = math.Max(b.stacked, top+h[i]) - low
		own[i] = top - low
		kids[i] = -low
	}

	x := make([]float64, n)
	y := make([]float64, n)
	band := make([]float64, n)
	side := make([]int8, n)
	// Running down: how far the trunk under each node has been drawn.
	trunk := make([]float64, n)

	if down {
		// Nodes are in depth-first order, which is the order of an outline's rows.
		row := 0.0
		for i := 0; i < n; i++ {
			if i > 0 && parent[i] == 0 {
				row += ROW_GAP
			}
			x[i] = float64(depth[i])*INDENT + w[i]/2
			y[i] = row
			row += h[i] + ROW_GAP
		}
	} else if n > 0 {
		// The first branches go right and the rest left, split where the two sides are closest in height.
		branches := nodes[0].children
		count := len(branches)
		total := 0.0
		for _, branch := range branches {
			total += extent[branch.id]
		}
		split := count
		best := math.Inf(1)
		right := 0.0
		for k := 1; k < count; k++ {
			right += extent[branches[k-1].id]
			diff := math.Abs(2*right - total + float64(2*k-count)*BRANCH_GAP_Y)
			if diff < best {
				best = diff
				split = k
			}
		}
		for from, to, dir := 0, split, int8(1); from < count; from, to, dir = to, count, -1 {
			middle := b.stack(branches, from, to, BRANCH_GAP_Y)
			for k := from; k < to; k++ {
				c := branches[k].id
				band[c] = rel[c] - middle
				side[c] = dir
			}
		}
		y[0] = -h[0] / 2
		for i := 0; i < n; i++ {
			if i > 0 {
				y[i] = band[i] + own[i]
			}
			for _, child := range nodes[i].children {
				c := child.id
				if i > 0 {
					side[c] = side[i]
					band[c] = band[i] + kids[i] + rel[c]
				}
				gap := float64(BRANCH_GAP_X)
				if i > 0 {
					gap = GAP_X
				}
				x[c] = x[i] + float64(side[c])*(w[i]/2+gap+w[c]/2)
			}
		}
	}

	minX := 0.0
	maxX := 0.0
	minY := 0.0
	maxY := 0.0
	g := &branch{}

	// The root is drawn last, on top of the links that leave it.
	for k := 1; k <= n; k++ {
		i := k % n
		node := nodes[i]
		shape := shapes[i]
		label := labels[i]
		cx := x[i]
		top := y[i]
		minX = math.Min(minX, cx-w[i]/2)
		maxX = math.Max(maxX, cx+w[i]/2)
		minY = math.Min(minY, top)
		maxY = math.Max(maxY, top+h[i])

		if i == 0 || parent[i] == 0 {
			g.flush(nodes[k-1].section)
		}
		if i > 0 && down {
			if parent[i] == 0 {
				g.color = svg.SeriesColor(node.section)
			}
			p := parent[i]
			// The link drops from under its parent and turns in to the node's left. It starts where
			// the link to the sibling above turned in, so each stretch of a shared trunk has one color.
			// Under the root's rounded end, the trunk starts where that end has come down to the bottom.
			stem := float64(STEM)
			if p == 0 {
				stem = math.Min(math.Max(STEM, h[0]/2), INDENT-BEND)
			}
			x1 := x[p] - w[p]/2 + math.Min(stem, w[p]/2)
			y1 := js.Or(trunk[p], y[p]+h[p])
			left := cx - w[i]/2
			x2 := left
			if shape != "" {
				x2 += svg.ShapeInset(shape, w[i], h[i], 3, 0)
			}
			y2 := top + anchor[i]
			bend := math.Max(0, math.Min(BEND, math.Min(x2-x1, y2-y1)))
			// The next link carries on from where this one leaves the trunk, so the trunk has no gap.
			trunk[p] = y2 - bend
			g.edges += `<path class="pele-edge" d="M` + num(x1) + `,` + num(y1) + `V` + num(y2-bend) + `Q` + num(x1) + `,` + num(y2) + ` ` + num(x1+bend) + `,` + num(y2) + `H` + num(x2) + `"/>`
		} else if i > 0 {
			if parent[i] == 0 {
				g.color = svg.SeriesColor(node.section)
			}
			p := parent[i]
			dir := float64(side[i])
			from := 3
			if dir > 0 {
				from = 1
			}
			inset := 0.0
			if shapes[p] != "" {
				inset = svg.ShapeInset(shapes[p], w[p], h[p], from, 0)
			}
			x1 := x[p] + dir*(w[p]/2-inset)
			y1 := y[p] + anchor[p]
			near := cx - (dir*w[i])/2
			x2 := near
			if shape != "" {
				x2 += dir * svg.ShapeInset(shape, w[i], h[i], 4-from, 0)
			}
			y2 := top + anchor[i]
			mid := num((x1 + x2) / 2)
			g.edges += `<path class="pele-edge" d="M` + num(x1) + `,` + num(y1) + `C` + mid + `,` + num(y1) + ` ` + mid + `,` + num(y2) + ` ` + num(x2) + `,` + num(y2) + `"/>`
		}

		pill := shape == "" && len(node.children) > 0
		// Every node off the root is filled with a tint of its branch's color and outlined in it,
		// whether its shape was given in the source or is the pill of a node that branches.
		// The root is neutral.
		tint := ` fill="` + g.color + `" fill-opacity="` + js.Num(TINT) + `" stroke="` + g.color + `"`
		inner := ""
		if shape != "" {
			if i > 0 {
				inner = svg.DrawShape(shape, w[i], h[i], tint, ` stroke="`+g.color+`"`)
			} else {
				inner = svg.DrawShape(shape, w[i], h[i], ` fill="var(--_s)" stroke="var(--_b)"`, ` stroke="var(--_b)"`)
			}
		} else if pill {
			inner = `<rect x="` + num(-w[i]/2) + `" y="` + num(-h[i]/2) + `" width="` + num(w[i]) + `" height="` + num(h[i]) + `" rx="` + num(h[i]/2) + `"` + tint + `/>`
		}
		shift := 0.0
		if node.icon != "" && drawn[i] {
			gap := 0.0
			if label.Width > 0 {
				gap = ICON_GAP
			}
			shift = (glyph + gap) / 2
			inner += common.IconSvg(node.icon, -shift-label.Width/2, dy[i]-glyph/2, glyph, icons)
		}
		kind := shape
		if kind == "" {
			if pill {
				kind = "branch"
			} else {
				kind = "text"
			}
		}
		root := ""
		if i == 0 {
			root = " pele-root"
		}
		var body string
		if down && shape == "" {
			body = leftLabel(label, shift-label.Width/2, dy[i], icons)
		} else {
			body = svg.LabelSvg(label, shift, dy[i], ` class="pele-label"`, icons)
		}
		group := `<g class="pele-node pele-shape-` + kind + root + svg.ClassNames(node.class) + `" data-id="` + svg.EscText(node.nodeId) +
			`" transform="translate(` + num(cx) + `,` + num(top+h[i]/2) + `)">` +
			inner + body +
			`</g>`
		if i > 0 {
			g.body += group
		} else {
			g.out += group
		}
	}

	title := text.LayoutLabel(model.title, false, measurer, size, common.TitleRoom(options), text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 16
	}
	width := math.Max(maxX-minX, title.Width)
	totalWidth := math.Ceil(width + 2*pad)
	totalHeight := math.Ceil(maxY - minY + titleHeight + 2*pad)
	content := svg.LabelSvg(title, totalWidth/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil) +
		`<g transform="translate(` + num(pad-minX+(width-maxX+minX)/2) + `,` + num(pad+titleHeight-minY) + `)">` + g.out + `</g>`

	return types.Rendered{
		SVG:    svg.SvgDocument("mindmap", totalWidth, totalHeight, size, options, svg.Accessible{}, content),
		Width:  totalWidth,
		Height: totalHeight,
		Links:  []types.LinkInfo{},
	}
}
