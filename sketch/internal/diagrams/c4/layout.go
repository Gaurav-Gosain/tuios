// Ported from pele src/diagrams/c4/layout.ts at 8aff183be57021b533f319a29341a529be934b08.

package c4

import (
	"math"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
)

const SHAPE_W = 216
const SHAPE_MIN_H = 64
const PAD_X = 14
const PAD_Y = 12

// Room between two columns for the label of a line across them, clear of an arrowhead on either side,
// with an arrowhead's length to spare for a word a little wider than the label room.
const GAP_X = NARROW + LABEL_PAD + 3*svg.ARROW
const GAP_Y = 88

// GROUP_PAD is room for a relation to run along the inside of a boundary.
const GROUP_PAD = LANE + 8
const GROUP_GAP = 48

// HEAD_LEFT and HEAD_TOP: where a boundary's heading starts, from its top-left corner.
const HEAD_LEFT = 16
const HEAD_TOP = 10

// FORMS: shape keywords Mermaid accepts in $shape, $sprite or $tags, as names of the shared shapes.
func FORMS(name string) string {
	// toLowerCase(): Go lowers U+0130 to "i" where JavaScript gives "i" and a combining
	// dot; a name with one matches no keyword in JavaScript.
	if strings.ContainsRune(name, 'İ') {
		return ""
	}
	switch strings.ToLower(name) {
	case "person":
		return "person"
	case "box", "rounded", "component":
		return "rect"
	case "cylinder", "database", "db":
		return "cyl"
	case "queue", "pipe":
		return "h-cyl"
	}
	return ""
}

type Node struct {
	Box
	shape *C4Shape
	// Type, name, technology, description.
	labels [4]text.Label
	textH  float64
}

type Group struct {
	Box
	boundary *C4Boundary
	kids     []*Group
	nodes    []*Node
	// Name, type, description.
	labels []text.Label
	head   float64
	headW  float64
	headX  float64
}

type Layout struct {
	// The boundaries that are drawn, parents before children. The first is the unnamed top level.
	order []*Group
	boxes map[*C4Element]*Box
}

func formOf(shape *C4Shape) string {
	explicit := FORMS(shape.shape)
	if explicit == "" {
		explicit = FORMS(shape.sprite)
	}
	if explicit != "" {
		return explicit
	}
	if shape.tags != "" {
		for _, tag := range strings.Split(shape.tags, ",") {
			if form := FORMS(js.Trim(tag)); form != "" {
				return form
			}
		}
	}
	kind := shape.kind
	switch {
	case strings.HasSuffix(kind, "person"):
		return "person"
	case strings.HasSuffix(kind, "_db"):
		return "cyl"
	case strings.HasSuffix(kind, "_queue"):
		return "h-cyl"
	}
	return "rect"
}

func measure(shape *C4Shape, measurer text.TextMeasurer, size, small, width float64) *Node {
	form := formOf(shape)
	capW := 0.0
	if form == "h-cyl" {
		capW = 36
	}
	capH := 0.0
	if form == "person" {
		capH = HEAD
	} else if form == "cyl" {
		capH = 24
	}
	inner := width - 2*PAD_X - capW
	techn := ""
	if shape.techn != "" {
		techn = "[" + shape.techn + "]"
	}
	labels := [4]text.Label{
		text.LayoutLabel("«"+strings.ReplaceAll(shape.kind, "_", " ")+"»", false, measurer, small-1, inner, 0),
		text.LayoutLabel(shape.label, false, measurer, size, inner, text.Bold),
		text.LayoutLabel(techn, false, measurer, small, inner, 0),
		text.LayoutLabel(shape.descr, false, measurer, small, inner, 0),
	}
	tw := 0.0
	textH := 0.0
	if labels[3].Height > 0 {
		textH = 4
	}
	for _, label := range labels {
		tw = math.Max(tw, label.Width)
		textH += label.Height
	}
	return &Node{
		shape:  shape,
		labels: labels,
		textH:  textH,
		Box: Box{
			form: form,
			w:    math.Max(width, tw+2*PAD_X+capW),
			h:    math.Max(SHAPE_MIN_H, textH+2*PAD_Y+capH),
		},
	}
}

// rows places boxes left to right in rows of `perRow`, from (0, 0), and returns the width and height the rows take.
// Shapes are stretched to the height of their row; boundaries keep their own.
func rows(boxes []*Box, perRow float64, gapX, gapY float64, stretch bool) (float64, float64) {
	width := 0.0
	y := 0.0
	// perRow is a whole number of at least 1; past the count of boxes, every count is one row.
	step := len(boxes)
	if perRow < float64(step) {
		step = int(perRow)
	}
	step = max(step, 1)
	for from := 0; from < len(boxes); from += step {
		to := min(len(boxes), from+step)
		x := 0.0
		rowH := 0.0
		for i := from; i < to; i++ {
			rowH = math.Max(rowH, boxes[i].h)
		}
		for i := from; i < to; i++ {
			box := boxes[i]
			box.x = x
			box.y = y
			if stretch {
				box.h = rowH
			}
			x += box.w + gapX
		}
		width = math.Max(width, x-gapX)
		y += rowH + gapY
	}
	if len(boxes) > 0 {
		return width, y - gapY
	}
	return width, 0
}

// layoutC4 is Mermaid's grid: in each boundary, its shapes in rows of c4ShapeInRow, then the boundaries inside it
// in rows of c4BoundaryInRow, each sized around its own content. room < 0 stands for undefined.
func layoutC4(db *C4Db, measurer text.TextMeasurer, size, small, room, gap float64) Layout {
	loose := room < 0
	groupPad := 12.0
	if loose {
		groupPad = GROUP_PAD
	}
	groups := make(map[*C4Boundary]*Group, len(db.boundaries))
	list := make([]*Group, 0, len(db.boundaries))
	for _, boundary := range db.boundaries {
		labelRoom := 4000.0
		if !loose {
			labelRoom = math.Max(80, room-2*HEAD_LEFT-2*GROUP_PAD*2)
		}
		var labels []text.Label
		if boundary.parent != nil {
			typ := ""
			if boundary.typ != "" {
				typ = "[" + boundary.typ + "]"
			}
			labels = []text.Label{
				text.LayoutLabel(boundary.label, false, measurer, size, labelRoom, text.Bold),
				text.LayoutLabel(typ, false, measurer, small, labelRoom, 0),
				text.LayoutLabel(boundary.descr, false, measurer, small, labelRoom, 0),
			}
		}
		head := 0.0
		headW := 0.0
		for _, label := range labels {
			head += label.Height
			headW = math.Max(headW, label.Width)
		}
		g := &Group{boundary: boundary, labels: labels, headW: headW, headX: HEAD_LEFT, Box: Box{form: "rect"}}
		if head > 0 {
			g.head = head + HEAD_TOP + 6 + LANE
		} else {
			g.head = groupPad
		}
		groups[boundary] = g
		list = append(list, g)
	}
	root := groups[db.boundaries[0]]
	for _, group := range list {
		if parent := group.boundary.parent; parent != nil {
			if p := groups[parent]; p != nil {
				p.kids = append(p.kids, group)
			}
		}
	}
	// A boundary that ends up inside itself is not reachable from the top, as in Mermaid.
	order := []*Group{root}
	depths := map[*C4Boundary]int{root.boundary: 0}
	for i := 0; i < len(order); i++ {
		for _, kid := range order[i].kids {
			depths[kid.boundary] = depths[order[i].boundary] + 1
			order = append(order, kid)
		}
	}
	for _, shape := range db.shapes {
		if shape.parent != nil {
			depth := depths[shape.parent]
			nodeWidth := float64(SHAPE_W)
			if !loose {
				nodeWidth = math.Max(100, math.Min(SHAPE_W, room-float64(2*float64(depth)*groupPad)-32))
			}
			if g := groups[shape.parent]; g != nil {
				g.nodes = append(g.nodes, measure(shape, measurer, size, small, nodeWidth))
			}
		}
	}

	perRow := math.Max(1, db.shapeInRow)
	groupsPerRow := math.Max(1, db.boundaryInRow)
	kidsTop := make(map[*Group]float64, len(order))
	var nodeBoxes, kidBoxes []*Box
	for i := len(order) - 1; i >= 0; i-- {
		group := order[i]
		gapX := float64(GAP_X)
		if !loose {
			gapX = gap
		}
		nodeBoxes = nodeBoxes[:0]
		for _, node := range group.nodes {
			nodeBoxes = append(nodeBoxes, &node.Box)
		}
		kidBoxes = kidBoxes[:0]
		for _, kid := range group.kids {
			kidBoxes = append(kidBoxes, &kid.Box)
		}
		count := func(boxes []*Box, limit, gap float64) float64 {
			if loose {
				return limit
			}
			widest := 1.0
			for _, box := range boxes {
				widest = math.Max(widest, box.w)
			}
			return math.Max(1, math.Min(limit, math.Floor((room+gap-float64(2*groupPad))/(widest+gap))))
		}
		gridW, gridH := rows(nodeBoxes, count(nodeBoxes, perRow, gapX), gapX, GAP_Y, true)
		kidsW, kidsH := rows(kidBoxes, count(kidBoxes, groupsPerRow, GROUP_GAP), GROUP_GAP, GROUP_GAP, false)
		between := 0.0
		if gridH > 0 && kidsH > 0 {
			between = GROUP_GAP
		}
		frame := groupPad
		if group == root {
			frame = 0
		}
		kidsTop[group] = gridH + between
		group.w = math.Max(math.Max(gridW, kidsW), group.headW+2*HEAD_LEFT-float64(2*frame)) + float64(2*frame)
		head := 0.0
		if frame != 0 {
			head = group.head
		}
		group.h = head + gridH + between + kidsH + frame
	}

	boxes := map[*C4Element]*Box{}
	for _, group := range order {
		left := group.x + groupPad
		top := group.y + group.head
		if group == root {
			left = group.x
			top = group.y
		}
		for _, node := range group.nodes {
			node.x += left
			node.y += top
			boxes[&node.shape.C4Element] = &node.Box
		}
		for _, kid := range group.kids {
			kid.x += left
			kid.y += top + kidsTop[group]
		}
		if group != root {
			boxes[&group.boundary.C4Element] = &group.Box
		}
	}
	return Layout{order: order, boxes: boxes}
}
