// Ported from pele src/diagrams/block/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package block

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
	GAP         = 12
	WRAP        = 200
	CORNER      = 8
	SHAPE_ATTRS = ` fill="var(--_s)" stroke="var(--_b)"`
	LINE_ATTRS  = ` stroke="var(--_b)"`
	// OBSTACLE_BUDGET: checking every edge against every block is quadratic, so large diagrams route without it.
	OBSTACLE_BUDGET = 2_000_000
	LABEL_GAP       = 4
)

// SQRT1_2 is Math.SQRT1_2.
const SQRT1_2 = 0.7071067811865476

var DEFAULT_CLASS = []string{"default"}

// shapeName is pele's SHAPES map. It is a switch rather than a map, which would allocate at init.
func shapeName(typ string) (string, bool) {
	switch typ {
	case "square":
		return "rect", true
	case "round":
		return "rounded", true
	case "circle":
		return "circle", true
	case "doublecircle":
		return "dbl-circ", true
	case "diamond":
		return "diam", true
	case "hexagon":
		return "hex", true
	case "stadium":
		return "stadium", true
	case "subroutine":
		return "fr-rect", true
	case "cylinder":
		return "cyl", true
	case "rect_left_inv_arrow":
		return "odd", true
	case "lean_right":
		return "lean-r", true
	case "lean_left":
		return "lean-l", true
	case "trapezoid":
		return "trap-b", true
	case "inv_trapezoid":
		return "trap-t", true
	}
	return "", false
}

// isFixed: shapes that keep their own size in a larger cell, where the others stretch to fill it.
func isFixed(shape string) bool {
	return shape == "circle" || shape == "dbl-circ" || shape == "diam"
}

// isWide: shapes that only widen, because their slants and curves are set by their height.
func isWide(shape string) bool {
	switch shape {
	case "stadium", "hex", "odd", "lean-r", "lean-l", "trap-b", "trap-t":
		return true
	}
	return false
}

type Kind int

const (
	Leaf Kind = iota
	Arrow
	Composite
	Space
)

type View struct {
	Cell
	block *Block
	kind  Kind
	index int
	// parent is the index in the render order of the composite that holds the view. Not in pele: sketch/text reads it.
	parent int
	shape  string
	label  text.Label
	style  svg.ResolvedStyle
	mask   int
	dx     float64
	dy     float64
	// Size as drawn, which for a fixed shape is smaller than its cell.
	bw float64
	bh float64
}

// Box is pele's Box: a position and size as drawn. Here a title's box is a View with only those set,
// so obstacles and titles share one list.
type Box = View

type Route struct {
	edge *Block
	// Two points for a straight line, three for a bend, none for a self-loop.
	points []float64
	loop   int
	label  text.Label
	lx     float64
	ly     float64
}

func isRound(v *View) bool {
	return v.kind == Leaf && (v.shape == "circle" || v.shape == "dbl-circ")
}

// edgeY is where a vertical line at x meets the outline, below the center (sign 1) or above it (sign -1).
func edgeY(v *View, x, sign float64) float64 {
	hw := v.bw / 2
	hh := v.bh / 2
	dx := math.Abs(x - v.x)
	reach := hh
	if isRound(v) {
		reach = math.Sqrt(math.Max(float64(hw*hw)-float64(dx*dx), 0))
	} else if v.kind == Leaf && v.shape == "diam" {
		reach = hh * math.Max(1-dx/hw, 0)
	} else if v.kind == Leaf {
		side := 0
		if sign > 0 {
			side = 2
		}
		reach = hh - svg.ShapeInset(v.shape, v.bw, v.bh, side, 0)
	} else if v.kind == Arrow && arrowAxis(v.mask) == 'x' {
		reach = hh - OVERHANG
	}
	return v.y + float64(sign*reach)
}

// edgeX is where a horizontal line at y meets the outline, right of the center (sign 1) or left of it (sign -1).
func edgeX(v *View, y, sign float64) float64 {
	hw := v.bw / 2
	hh := v.bh / 2
	dy := math.Abs(y - v.y)
	reach := hw
	if isRound(v) {
		reach = math.Sqrt(math.Max(float64(hh*hh)-float64(dy*dy), 0))
	} else if v.kind == Leaf && v.shape == "diam" {
		reach = hw * math.Max(1-dy/hh, 0)
	} else if v.kind == Leaf {
		side := 3
		if sign > 0 {
			side = 1
		}
		reach = hw - svg.ShapeInset(v.shape, v.bw, v.bh, side, 0)
	} else if v.kind == Arrow && arrowAxis(v.mask) == 'y' {
		reach = hw - OVERHANG
	}
	return v.x + float64(sign*reach)
}

// border is where the line from the center toward (tx, ty) leaves the outline.
func border(v *View, tx, ty float64, out []float64) []float64 {
	dx := tx - v.x
	dy := ty - v.y
	hw := v.bw / 2
	hh := v.bh / 2
	var k float64
	if isRound(v) {
		k = hw / js.Or(js.Hypot(dx, dy), 1)
	} else if v.kind == Leaf && v.shape == "diam" {
		k = 1 / js.Or(math.Abs(dx)/hw+math.Abs(dy)/hh, 1)
	} else {
		a, b := math.Inf(1), math.Inf(1)
		if dx != 0 {
			a = hw / math.Abs(dx)
		}
		if dy != 0 {
			b = hh / math.Abs(dy)
		}
		k = math.Min(a, b)
	}
	if math.IsInf(k, 1) {
		k = 0
	}
	return append(out, v.x+float64(dx*k), v.y+float64(dy*k))
}

func crossesBox(x1, y1, x2, y2 float64, v *View) bool {
	l := v.x - v.bw/2 + 1
	r := v.x + v.bw/2 - 1
	t := v.y - v.bh/2 + 1
	b := v.y + v.bh/2 - 1
	if (x1 < l && x2 < l) || (x1 > r && x2 > r) || (y1 < t && y2 < t) || (y1 > b && y2 > b) {
		return false
	}
	if x1 == x2 || y1 == y2 {
		return true
	}
	// The segment is diagonal and its bounding box overlaps: the box is hit unless all four corners are on one side.
	dx := x2 - x1
	dy := y2 - y1
	side := func(x, y float64) float64 { return js.Sign(float64(dx*(y-y1)) - float64(dy*(x-x1))) }
	first := side(l, t)
	return first == 0 || side(r, t) != first || side(r, b) != first || side(l, b) != first
}

func hits(points []float64, s, t *View, obstacles []*View) int {
	count := 0
	for _, v := range obstacles {
		if v == s || v == t {
			continue
		}
		for i := 0; i+3 < len(points); i += 2 {
			if crossesBox(points[i], points[i+1], points[i+2], points[i+3], v) {
				count++
				break
			}
		}
	}
	return count
}

// routeEdge: a straight line where the two blocks share a row or a column, otherwise one bend between facing sides.
func routeEdge(s, t *View, shift float64, obstacles []*View, check bool) []float64 {
	sl := s.x - s.bw/2
	sr := s.x + s.bw/2
	st := s.y - s.bh/2
	sb := s.y + s.bh/2
	tl := t.x - t.bw/2
	tr := t.x + t.bw/2
	tt := t.y - t.bh/2
	tb := t.y + t.bh/2
	x1 := math.Max(sl, tl)
	x2 := math.Min(sr, tr)
	y1 := math.Max(st, tt)
	y2 := math.Min(sb, tb)
	overX := x2-x1 > 6
	overY := y2-y1 > 6

	if overX && overY {
		// One block is inside the other: join the inner one to the nearest side of the outer one.
		inner, outer := t, s
		if s.bw*s.bh <= t.bw*t.bh {
			inner, outer = s, t
		}
		up := inner.y - inner.bh/2 - (outer.y - outer.bh/2)
		down := outer.y + outer.bh/2 - (inner.y + inner.bh/2)
		left := inner.x - inner.bw/2 - (outer.x - outer.bw/2)
		right := outer.x + outer.bw/2 - (inner.x + inner.bw/2)
		least := math.Min(math.Min(up, down), math.Min(left, right))
		var points []float64
		if least == down {
			points = []float64{inner.x, edgeY(inner, inner.x, 1), inner.x, outer.y + outer.bh/2}
		} else if least == up {
			points = []float64{inner.x, edgeY(inner, inner.x, -1), inner.x, outer.y - outer.bh/2}
		} else if least == right {
			points = []float64{edgeX(inner, inner.y, 1), inner.y, outer.x + outer.bw/2, inner.y}
		} else {
			points = []float64{edgeX(inner, inner.y, -1), inner.y, outer.x - outer.bw/2, inner.y}
		}
		if inner == s {
			return points
		}
		return []float64{points[2], points[3], points[0], points[1]}
	}
	if overX {
		margin := math.Min(6, (x2-x1)/2)
		x := math.Min(math.Max((x1+x2)/2+shift, x1+margin), x2-margin)
		down := -1.0
		if t.y > s.y {
			down = 1
		}
		return []float64{x, edgeY(s, x, down), x, edgeY(t, x, -down)}
	}
	if overY {
		margin := math.Min(6, (y2-y1)/2)
		y := math.Min(math.Max((y1+y2)/2+shift, y1+margin), y2-margin)
		right := -1.0
		if t.x > s.x {
			right = 1
		}
		return []float64{edgeX(s, y, right), y, edgeX(t, y, -right), y}
	}

	right := -1.0
	if t.x > s.x {
		right = 1
	}
	down := -1.0
	if t.y > s.y {
		down = 1
	}
	across := []float64{edgeX(s, s.y, right), s.y, t.x, s.y, t.x, edgeY(t, t.x, -down)}
	along := []float64{s.x, edgeY(s, s.x, down), s.x, t.y, edgeX(t, t.y, -right), t.y}
	wide := math.Abs(t.x-s.x) >= math.Abs(t.y-s.y)
	best, other := along, across
	if wide {
		best, other = across, along
	}
	if !check {
		return best
	}
	least := hits(best, s, t, obstacles)
	if least == 0 {
		return best
	}
	if count := hits(other, s, t, obstacles); count < least {
		best = other
		least = count
	}
	if least == 0 {
		return best
	}
	direct := make([]float64, 0, 4)
	direct = border(s, t.x, t.y, direct)
	direct = border(t, s.x, s.y, direct)
	if hits(direct, s, t, obstacles) < least {
		return direct
	}
	return best
}

// trace builds the path through two or three points, rounding the bend and leaving room for markers.
func trace(p []float64, startTrim, endTrim float64) svg.EdgePath {
	n := len(p)
	sx := p[0]
	sy := p[1]
	ex := p[n-2]
	ey := p[n-1]
	ax := p[2] - sx
	ay := p[3] - sy
	first := js.Hypot(ax, ay)
	ax /= js.Or(first, 1)
	ay /= js.Or(first, 1)
	bx := ex - p[n-4]
	by := ey - p[n-3]
	last := js.Hypot(bx, by)
	bx /= js.Or(last, 1)
	by /= js.Or(last, 1)
	if n == 4 && startTrim+endTrim > first {
		scale := first / (startTrim + endTrim)
		startTrim *= scale
		endTrim *= scale
	}
	startTrim = math.Min(startTrim, first)
	endTrim = math.Min(endTrim, last)
	var d strings.Builder
	d.WriteByte('M')
	svg.WriteNum(&d, sx+float64(ax*startTrim))
	d.WriteByte(',')
	svg.WriteNum(&d, sy+float64(ay*startTrim))
	if n == 6 {
		r := math.Max(0, math.Min(math.Min(CORNER, first-startTrim), last-endTrim))
		d.WriteByte('L')
		svg.WriteNum(&d, p[2]-float64(ax*r))
		d.WriteByte(',')
		svg.WriteNum(&d, p[3]-float64(ay*r))
		d.WriteByte('Q')
		svg.WriteNum(&d, p[2])
		d.WriteByte(',')
		svg.WriteNum(&d, p[3])
		d.WriteByte(' ')
		svg.WriteNum(&d, p[2]+float64(bx*r))
		d.WriteByte(',')
		svg.WriteNum(&d, p[3]+float64(by*r))
	}
	d.WriteByte('L')
	svg.WriteNum(&d, ex-float64(bx*endTrim))
	d.WriteByte(',')
	svg.WriteNum(&d, ey-float64(by*endTrim))
	return svg.EdgePath{D: d.String(), Sx: sx, Sy: sy, Sdx: -ax, Sdy: -ay, Ex: ex, Ey: ey, Edx: bx, Edy: by}
}

// loopArc: a self-loop is an arc around the top right corner of the block, or around the matching point on a
// round or diamond outline. Gives the arc's center and radius and the angles where it leaves and returns.
func loopArc(v *View, k int) (px, py, r, a1, a2 float64) {
	hw := v.bw / 2
	hh := v.bh / 2
	r = math.Max(1, math.Min(math.Min(10+float64(5*float64(k-1)), hw), hh))
	if isRound(v) {
		px := v.x + float64(hw*SQRT1_2)
		py := v.y - float64(hw*SQRT1_2)
		half := 2 * js.Asin(math.Min(1, r/(2*hw)))
		angle := func(at float64) float64 {
			return js.Atan2(v.y-float64(hw*js.Sin(at))-py, v.x+float64(hw*js.Cos(at))-px)
		}
		return px, py, r, angle(math.Pi/4 + half), angle(math.Pi/4 - half)
	}
	if v.kind == Leaf && v.shape == "diam" {
		slope := js.Atan2(hh, hw)
		return v.x + hw/2, v.y - hh/2, r, slope + math.Pi, slope
	}
	return v.x + hw, v.y - hh, r, math.Pi, math.Pi / 2
}

func loopPath(v *View, k int, startTrim, endTrim float64) svg.EdgePath {
	px, py, r, a1, a2 := loopArc(v, k)
	from := a1 + startTrim/r
	to := a2 - endTrim/r
	sweep := math.Mod(to-from, 2*math.Pi)
	if sweep <= 0 {
		sweep += 2 * math.Pi
	}
	x1 := px + float64(r*js.Cos(from))
	y1 := py + float64(r*js.Sin(from))
	x2 := px + float64(r*js.Cos(to))
	y2 := py + float64(r*js.Sin(to))
	sx := px + float64(r*js.Cos(a1))
	sy := py + float64(r*js.Sin(a1))
	ex := px + float64(r*js.Cos(a2))
	ey := py + float64(r*js.Sin(a2))
	// Each marker lies along the chord it covers, so it follows the curve instead of the tangent at its tip.
	sl := js.Hypot(sx-x1, sy-y1)
	el := js.Hypot(ex-x2, ey-y2)
	large := "0"
	if sweep > math.Pi {
		large = "1"
	}
	path := svg.EdgePath{
		D:  "M" + svg.Num(x1) + "," + svg.Num(y1) + "A" + svg.Num(r) + "," + svg.Num(r) + " 0 " + large + " 1 " + svg.Num(x2) + "," + svg.Num(y2),
		Sx: sx,
		Sy: sy,
		Ex: ex,
		Ey: ey,
	}
	if sl > 0 {
		path.Sdx, path.Sdy = (sx-x1)/sl, (sy-y1)/sl
	} else {
		path.Sdx, path.Sdy = js.Sin(a1), -js.Cos(a1)
	}
	if el > 0 {
		path.Edx, path.Edy = (ex-x2)/el, (ey-y2)/el
	} else {
		path.Edx, path.Edy = -js.Sin(a2), js.Cos(a2)
	}
	return path
}

var SPOTS = [...]float64{0.5, 0.3, 0.7, 0.15, 0.85}

// labelSpot picks the point on a leg where a label of the given size covers no block other than the two it joins.
func covers(x, y, w, h float64, s, t *View, obstacles []*Box, check bool) bool {
	if !check {
		return false
	}
	for _, v := range obstacles {
		if v != s && v != t && math.Abs(x-v.x) < (w+v.bw)/2 && math.Abs(y-v.y) < (h+v.bh)/2 {
			return true
		}
	}
	return false
}

// labelSpot picks the point on a leg where a label of the given size covers no block other than the two it joins.
func labelSpot(p []float64, at int, w, h float64, s, t *View, obstacles []*View, check bool) float64 {
	if !check {
		return 0.5
	}
	for _, spot := range SPOTS {
		x := p[at] + float64((p[at+2]-p[at])*spot)
		y := p[at+1] + float64((p[at+3]-p[at+1])*spot)
		if !covers(x, y, w, h, s, t, obstacles, check) {
			return spot
		}
	}
	return 0.5
}

// stackLabels stacks overlapping labels along their shared line, if they fit between its markers and cover no other block or title.
// Returns the extra length needed to fit the labels.
func stackLabels(stack []int, routes []Route, legs [][]float64, s, t *View, obstacles []*Box, check bool) float64 {
	vertical := legs[stack[0]][0] == legs[stack[0]][2]
	lo := math.Inf(-1)
	hi := math.Inf(1)
	need := float64(-LABEL_GAP)
	crowded := false
	for a := range stack {
		leg := legs[stack[a]]
		if vertical {
			if leg[0] != leg[2] {
				return 0
			}
			lo = math.Max(lo, math.Min(leg[1], leg[3]))
			hi = math.Min(hi, math.Max(leg[1], leg[3]))
		} else {
			if leg[1] != leg[3] {
				return 0
			}
			lo = math.Max(lo, math.Min(leg[0], leg[2]))
			hi = math.Min(hi, math.Max(leg[0], leg[2]))
		}
		p := &routes[stack[a]]
		if vertical {
			need += p.label.Height + LABEL_GAP
		} else {
			need += p.label.Width + 8 + LABEL_GAP
		}
		for b := 0; b < a; b++ {
			q := &routes[stack[b]]
			if math.Abs(p.lx-q.lx) < (p.label.Width+q.label.Width)/2+8 && math.Abs(p.ly-q.ly) < (p.label.Height+q.label.Height)/2 {
				crowded = true
			}
		}
	}
	if !crowded {
		return 0
	}
	if need > hi-lo-2*svg.ARROW {
		return need - (hi - lo - 2*svg.ARROW)
	}
	along := make([]float64, 0, len(stack))
	next := (lo + hi - need) / 2
	for _, i := range stack {
		label, lx, ly := routes[i].label, routes[i].lx, routes[i].ly
		size := label.Width + 8
		if vertical {
			size = label.Height
		}
		at := next + size/2
		x, y := at, ly
		if vertical {
			x, y = lx, at
		}
		if covers(x, y, label.Width+8, label.Height, s, t, obstacles, check) {
			return 0
		}
		along = append(along, at)
		next += size + LABEL_GAP
	}
	for k, i := range stack {
		if vertical {
			routes[i].ly = along[k]
		} else {
			routes[i].lx = along[k]
		}
	}
	return 0
}

// gapsKey names one gap list for widen's base. pele keys the map by the array itself. Not in pele.
type gapsKey struct {
	box      *View
	vertical bool
}

// widen widens the gap in the blocks' nearest shared container.
// `base` keeps the gaps as they were when the labels were measured.
func widen(parents []*View, s, t *View, vertical bool, short float64, base map[gapsKey][]float64) bool {
	parentOf := func(v *View) *View {
		if v.index >= len(parents) {
			return nil
		}
		return parents[v.index]
	}
	chain := map[*View]bool{}
	for v := s; v != nil; v = parentOf(v) {
		chain[v] = true
	}
	var tb *View
	box := t
	for box != nil && !chain[box] {
		tb = box
		box = parentOf(box)
	}
	if box == nil || tb == nil || box == s {
		return false
	}
	sb := s
	for parentOf(sb) != box {
		sb = parentOf(sb)
	}
	var at int
	var gaps *[]float64
	if vertical {
		if sb.row == tb.row {
			return false
		}
		at = max(sb.row, tb.row) - 1
		if box.rowGaps == nil {
			box.rowGaps = []float64{}
		}
		gaps = &box.rowGaps
	} else {
		l, r := tb, sb
		if sb.col < tb.col {
			l, r = sb, tb
		}
		if r.col < l.col+l.span {
			return false
		}
		at = int(r.col - 1)
		if box.colGaps == nil {
			box.colGaps = []float64{}
		}
		gaps = &box.colGaps
	}
	key := gapsKey{box, vertical}
	if _, ok := base[key]; !ok {
		base[key] = append([]float64{}, (*gaps)...)
	}
	for len(*gaps) <= at {
		*gaps = append(*gaps, 0)
	}
	(*gaps)[at] = math.Max((*gaps)[at], gapAt(base[key], at)+short)
	return true
}

func renderBlock(model *BlockDb, config types.Config, options types.RenderOptions) types.Rendered {
	return common.Tighten(options, func(tight bool) types.Rendered { return draw(model, config, options, tight) })
}

func draw(model *BlockDb, config types.Config, options types.RenderOptions, tight bool) types.Rendered {
	size := types.Or(options.FontSize, 16)
	edgeSize := js.Round(size * 0.875)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)
	icons := options.Icons
	gap := float64(GAP)
	if conf, ok := config["block"].(map[string]any); ok {
		if configured, ok := conf["padding"].(float64); ok && configured >= 0 {
			gap = math.Min(configured, 200)
		}
	}
	if tight {
		gap = math.Min(gap, 6)
	}
	wrap := float64(WRAP)
	if tight {
		wrap = 80
	}

	styleOf := func(block *Block) svg.ResolvedStyle {
		var declarations []string
		// A style list is split on commas only, so one entry can still hold several declarations.
		add := func(styles []string) {
			for _, style := range styles {
				declarations = append(declarations, strings.Split(style, ";")...)
			}
		}
		names := DEFAULT_CLASS
		if len(block.classes) > 0 {
			names = block.classes
		}
		for _, name := range names {
			if def := model.classes[name]; def != nil {
				add(def.styles)
			}
		}
		add(block.styles)
		return svg.ResolveStyle(declarations)
	}

	view := func(block *Block, index int) *View {
		typ := block.typ
		var style svg.ResolvedStyle
		if typ != "space" {
			style = styleOf(block)
		}
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
		v := &View{block: block, index: index}
		v.columns = -1
		v.heights = nil
		switch typ {
		case "space":
			v.kind = Space
			v.label = text.EMPTY
		case "composite":
			v.kind = Composite
			room := 4000.0
			if tight {
				room = common.TitleRoom(options)
			}
			v.label = text.LayoutLabel(block.label, false, measurer, fontSize, room, textStyle)
			if v.label.Height > 0 {
				v.head = v.label.Height + 4
			}
			v.nw = v.label.Width + float64(2*gap)
			v.nh = v.head + float64(2*gap)
		case "block_arrow":
			v.kind = Arrow
			v.label = text.LayoutLabel(block.label, false, measurer, fontSize, wrap, textStyle)
			blank := true
			for _, line := range v.label.Lines {
				for _, part := range line {
					if part.Icon != "" || js.Trim(part.Text) != "" {
						blank = false
					}
				}
			}
			v.mask = arrowMask(block.directions)
			var s ArrowSize
			if blank {
				s = arrowSize(v.mask, 0, 0)
			} else {
				s = arrowSize(v.mask, v.label.Width, v.label.Height)
			}
			v.nw, v.nh, v.dx, v.dy = s.w, s.h, s.dx, s.dy
		default:
			shape, ok := shapeName(typ)
			if !ok {
				shape = "rect"
			}
			v.shape = shape
			v.label = text.LayoutLabel(block.label, false, measurer, fontSize, wrap, textStyle)
			s := svg.ShapeSizeOf(shape, v.label.Width, v.label.Height)
			v.nw = s.W
			// A cylinder's caps deepen as it widens, so it is sized for the deepest ones.
			v.nh = s.H
			if shape == "cyl" {
				v.nh = math.Max(s.H, v.label.Height+40)
			}
			v.dy = s.Dy
		}
		v.style = style
		if v.kind == Composite {
			v.columns = block.columns
		}
		if v.kind == Space {
			v.span = 1
		} else {
			v.span = block.widthInColumns
		}
		v.space = v.kind == Space
		return v
	}

	root := view(model.root, 0)
	root.nw = 0
	root.nh = 0
	order := []*View{root}
	parents := []*View{nil}
	byId := map[string]*View{}
	for i := 0; i < len(order); i++ {
		parent := order[i]
		if parent.kind != Composite {
			continue
		}
		for _, child := range parent.block.children {
			v := view(child, len(order))
			v.parent = i
			parent.children = append(parent.children, &v.Cell)
			parents = append(parents, parent)
			order = append(order, v)
			if v.kind != Space {
				byId[child.id] = v
			}
		}
	}

	var obstacles []*View
	var titles []*Box
	// Layout keeps the widest a container has been, so each run starts from the widths it needs.
	widths := make([]float64, len(order))
	for _, v := range order {
		widths[v.index] = v.nw
	}
	place := func() (float64, float64) {
		for _, v := range order {
			v.nw = widths[v.index]
		}
		contentWidth, contentHeight := layout(&root.Cell, gap)
		obstacles = obstacles[:0]
		titles = titles[:0]
		for _, v := range order {
			if v.kind == Leaf && isFixed(v.shape) {
				v.bw = v.nw
				v.bh = v.nh
			} else if v.kind == Arrow {
				v.bw = v.nw
				if v.span > 1 {
					v.bw = v.w
				}
				v.bh = v.nh
			} else {
				v.bw = v.w
				v.bh = v.h
				if v.kind == Leaf && isWide(v.shape) {
					v.bh = v.nh
				}
			}
			if v.kind == Leaf && v.shape == "cyl" {
				v.dy = math.Min(12, math.Max(6, v.bw/14)) / 2
			}
			if v.kind == Leaf || v.kind == Arrow {
				obstacles = append(obstacles, v)
			} else if v.head > 0 {
				title := &Box{bw: v.label.Width, bh: v.label.Height}
				title.x = v.x
				title.y = v.y - v.h/2 + (v.head+gap)/2 + 2
				titles = append(titles, title)
			}
		}
		return contentWidth, contentHeight
	}
	contentWidth, contentHeight := place()

	// Edges between the same two blocks are spread apart instead of drawn on top of each other.
	pairs := map[int]int{}
	labelled := map[int]bool{}
	var ends [][2]*View
	var drawn []*Block
	pairKey := func(s, t *View) int {
		return min(s.index, t.index)*len(order) + max(s.index, t.index)
	}
	for _, edge := range model.edges {
		s := byId[edge.start]
		t := byId[edge.end]
		if s == nil || t == nil {
			continue
		}
		key := pairKey(s, t)
		pairs[key]++
		if edge.label != "" {
			labelled[key] = true
		}
		ends = append(ends, [2]*View{s, t})
		drawn = append(drawn, edge)
	}
	check := len(drawn)*len(obstacles) <= OBSTACLE_BUDGET
	routes := make([]Route, 0, len(drawn))
	// stacks is pele's Map; stackKeys keeps its insertion order.
	stacks := map[int][]int{}
	var stackKeys []int
	legs := make([][]float64, len(drawn))
	route := func() {
		seen := map[int]int{}
		routes = routes[:0]
		clear(stacks)
		stackKeys = stackKeys[:0]
		for i := range drawn {
			s, t := ends[i][0], ends[i][1]
			key := pairKey(s, t)
			total, ok := pairs[key]
			if !ok {
				total = 1
			}
			k := seen[key]
			seen[key] = k + 1
			label := text.LayoutLabel(drawn[i].label, false, measurer, edgeSize, WRAP, 0)
			var points []float64
			var lx, ly float64
			if s == t {
				px, py, r, _, _ := loopArc(s, k+1)
				lx = px + r + 6 + label.Width/2
				ly = py - r/2
			} else {
				step := 14.0
				if labelled[key] {
					step = js.Round(edgeSize*1.5) + 2
				}
				points = routeEdge(s, t, float64((float64(k)-float64(total-1)/2)*step), obstacles, check)
				n := len(points)
				// The label goes on the longer leg.
				firstLeg := math.Abs(points[2]-points[0]) + math.Abs(points[3]-points[1])
				lastLeg := math.Abs(points[n-2]-points[n-4]) + math.Abs(points[n-1]-points[n-3])
				at := 0
				if n == 6 && lastLeg > firstLeg {
					at = 2
				}
				spot := 0.5
				if label.Width > 0 {
					spot = labelSpot(points, at, label.Width+8, label.Height, s, t, obstacles, check)
				}
				lx = points[at] + float64((points[at+2]-points[at])*spot)
				ly = points[at+1] + float64((points[at+3]-points[at+1])*spot)
				if label.Width > 0 && total > 1 {
					legs[i] = append([]float64(nil), points[at:at+4]...)
					if _, ok := stacks[key]; !ok {
						stackKeys = append(stackKeys, key)
					}
					stacks[key] = append(stacks[key], i)
				}
			}
			loop := 0
			if s == t {
				loop = k + 1
			}
			routes = append(routes, Route{edge: drawn[i], points: points, loop: loop, label: label, lx: lx, ly: ly})
		}
	}

	settle := func(grow bool) bool {
		route()
		var blocked []*Box
		if check {
			blocked = append(append(blocked, obstacles...), titles...)
		}
		base := map[gapsKey][]float64{}
		grown := false
		for _, key := range stackKeys {
			stack := stacks[key]
			if len(stack) < 2 {
				continue
			}
			s, t := ends[stack[0]][0], ends[stack[0]][1]
			short := stackLabels(stack, routes, legs, s, t, blocked, check)
			vertical := legs[stack[0]][0] == legs[stack[0]][2]
			// Keep columns narrow to fit the host in tight layouts.
			if grow && short > 0 && (vertical || !tight) && widen(parents, s, t, vertical, math.Ceil(short), base) {
				grown = true
			}
		}
		return grown
	}
	overlap := func() float64 {
		area := 0.0
		for i := range routes {
			p := &routes[i]
			for j := i + 1; j < len(routes); j++ {
				q := &routes[j]
				pw := (p.label.Width + 8) / 2
				qw := (q.label.Width + 8) / 2
				w := math.Min(p.lx+pw, q.lx+qw) - math.Max(p.lx-pw, q.lx-qw)
				h := math.Min(p.ly+p.label.Height/2, q.ly+q.label.Height/2) - math.Max(p.ly-p.label.Height/2, q.ly-q.label.Height/2)
				if p.label.Width > 0 && q.label.Width > 0 && w > 0 && h > 0 {
					area += float64(w * h)
				}
			}
		}
		return area
	}
	minX := 0.0
	minY := 0.0
	maxX := 0.0
	maxY := 0.0
	grow := func(x, y, w, h float64) {
		if x-w/2 < minX {
			minX = x - w/2
		}
		if x+w/2 > maxX {
			maxX = x + w/2
		}
		if y-h/2 < minY {
			minY = y - h/2
		}
		if y+h/2 > maxY {
			maxY = y + h/2
		}
	}
	// bound finds the drawing's extent and gives its width.
	bound := func() float64 {
		minX = 0
		minY = 0
		maxX = contentWidth
		maxY = contentHeight
		for i := range routes {
			route := &routes[i]
			if route.loop > 0 {
				px, py, r, _, _ := loopArc(ends[i][0], route.loop)
				grow(px, py, float64(2*r)+2, float64(2*r)+2)
			}
			if route.label.Width > 0 {
				grow(route.lx, route.ly, route.label.Width+8, route.label.Height)
			}
		}
		return maxX - minX
	}
	if settle(len(drawn)*len(drawn) <= OBSTACLE_BUDGET) {
		before := overlap()
		narrow := bound()
		// A container stretched by its row gives some of a new gap back, so the gap may take a few rounds.
		grown := true
		for round := 0; round < 3 && grown; round++ {
			contentWidth, contentHeight = place()
			grown = settle(round < 2)
		}
		// The drawing only grows if that leaves less of the labels covering each other, and within maxWidth.
		wide := bound()
		room := math.Inf(1)
		if options.MaxWidth != nil && *options.MaxWidth > 0 {
			room = *options.MaxWidth
		}
		if overlap() > before-1 || (wide > narrow && math.Ceil(wide+float64(2*pad)) > room) {
			for _, v := range order {
				v.rowGaps = nil
				v.colGaps = nil
			}
			contentWidth, contentHeight = place()
			settle(false)
		}
	}

	bound()

	titleRoom := 4000.0
	if tight {
		titleRoom = common.TitleRoom(options)
	}
	title := svg.TitleLabel(model.title, measurer, size, titleRoom)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 12
	}
	inner := math.Max(maxX-minX, title.Width)
	ox := pad - minX + (inner-(maxX-minX))/2
	oy := pad + titleHeight - minY
	for _, v := range order {
		v.x += ox
		v.y += oy
	}
	width := math.Ceil(inner + float64(2*pad))
	height := math.Ceil(maxY - minY + titleHeight + float64(2*pad))

	var clusters, nodes strings.Builder
	var fills []svg.Fill
	for i := 1; i < len(order); i++ {
		v := order[i]
		if v.kind == Space {
			continue
		}
		classes := svg.ClassNames(strings.Join(v.block.classes, " "))
		id := svg.EscText(v.block.id)
		if v.kind == Composite {
			top := v.y - v.h/2
			if svg.Paints(v.style.Shape) {
				fills = append(fills, svg.Fill{Box: [4]float64{v.x - v.w/2, top, v.x + v.w/2, top + v.h}, Shape: v.style.Shape})
			}
			svg.WriteStrings(&clusters, `<g class="pele-cluster`, classes, `" data-id="`, id, `"><rect x="`)
			svg.WriteNum(&clusters, v.x-v.w/2)
			clusters.WriteString(`" y="`)
			svg.WriteNum(&clusters, top)
			clusters.WriteString(`" width="`)
			svg.WriteNum(&clusters, v.w)
			clusters.WriteString(`" height="`)
			svg.WriteNum(&clusters, v.h)
			opacity := ` fill-opacity="0.5"`
			if svg.SetsFill(v.style.Shape) {
				opacity = ""
			}
			svg.WriteStrings(&clusters, `" fill="var(--_a)"`, opacity, ` stroke="var(--_b)"`, svg.Rounded(v.style.Shape), `/>`,
				svg.LabelSvg(v.label, v.x, top+(v.head+gap)/2+2, ` class="pele-cluster-label"`+svg.InkFill(v.style, "var(--_m)")+v.style.Text, icons),
				`</g>`)
			continue
		}
		arrow := v.kind == Arrow
		shapeClass := v.shape
		if arrow {
			shapeClass = "block-arrow"
		}
		svg.WriteStrings(&nodes, `<g class="pele-node pele-shape-`, shapeClass, classes, `" data-id="`, id, `" transform="translate(`)
		svg.WriteNum(&nodes, v.x)
		nodes.WriteByte(',')
		svg.WriteNum(&nodes, v.y)
		nodes.WriteString(`)">`)
		if arrow {
			nodes.WriteString(drawArrow(v.mask, v.bw, v.bh, SHAPE_ATTRS+v.style.Shape))
		} else {
			nodes.WriteString(svg.DrawShape(v.shape, v.bw, v.bh, SHAPE_ATTRS+v.style.Shape, LINE_ATTRS+v.style.Line))
		}
		ink := ""
		if arrow || svg.ShapeFilled(v.shape) {
			ink = svg.InkFill(v.style, "")
		}
		svg.WriteStrings(&nodes, svg.LabelSvg(v.label, v.dx, v.dy, ` class="pele-label"`+ink+v.style.Text, icons), `</g>`)
	}

	var casings, edges, labels strings.Builder
	for i := range routes {
		route := &routes[i]
		edge, points := route.edge, route.points
		startType := edge.arrowTypeStart
		endType := edge.arrowTypeEnd
		var path svg.EdgePath
		if route.loop > 0 {
			path = loopPath(ends[i][0], route.loop, svg.MarkerTrim(startType), svg.MarkerTrim(endType))
		} else {
			for k := 0; k < len(points); k += 2 {
				points[k] += ox
				points[k+1] += oy
			}
			path = trace(points, svg.MarkerTrim(startType), svg.MarkerTrim(endType))
		}
		over, onFill := svg.OverFills(path.D, []float64{path.Sx, path.Sy, path.Ex, path.Ey}, fills, "", nil)
		if onFill && over == "" {
			casings.WriteString(svg.EdgeCasing(path, startType, endType, "", edge.thickness == "thick"))
		}
		id := svg.EscText(edge.id)
		svg.WriteStrings(&edges, `<g class="pele-edge" data-id="`, id, `"`)
		if over != "" {
			svg.WriteStrings(&edges, ` stroke="`, over, `"`)
		}
		svg.WriteStrings(&edges, `><path d="`, path.D, `"`)
		if edge.thickness == "thick" {
			edges.WriteString(` stroke-width="2.5"`)
		}
		if edge.pattern == "dotted" {
			edges.WriteString(` stroke-dasharray="3 4"`)
		}
		edges.WriteString(`/>`)
		markerColor := over
		if markerColor == "" {
			markerColor = "var(--_l)"
		}
		svg.WriteMarker(&edges, startType, path.Sx, path.Sy, path.Sdx, path.Sdy, markerColor)
		svg.WriteMarker(&edges, endType, path.Ex, path.Ey, path.Edx, path.Edy, markerColor)
		edges.WriteString(`</g>`)
		if route.label.Width > 0 {
			x := route.lx + ox
			y := route.ly + oy
			labels.WriteString(svg.EdgeLabelSvg(edge.id, route.label, x, y, "", icons))
		}
	}

	parts := []string{svg.LabelSvg(title, width/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil)}
	if clusters.Len() > 0 {
		parts = append(parts, `<g class="pele-clusters">`, clusters.String(), `</g>`)
	}
	parts = append(parts, svg.CasingsSvg(casings.String()))
	if edges.Len() > 0 {
		parts = append(parts, `<g class="pele-edges" fill="none" stroke="var(--_l)" stroke-linecap="round">`, edges.String(), `</g>`)
	}
	parts = append(parts, `<g class="pele-nodes">`, nodes.String(), `</g>`)
	if labels.Len() > 0 {
		parts = append(parts, `<g class="pele-edge-labels" font-size="`, svg.Num(edgeSize), `">`, labels.String(), `</g>`)
	}
	out := svg.SvgDocument("block", width, height, size, options, svg.Accessible{}, parts...)

	rendered := types.Rendered{SVG: out, Width: width, Height: height, Links: []types.LinkInfo{}}
	if options.Geometry != nil {
		rendered.Geometry = geometry(order, routes, ends, ox, oy, width, height)
	}
	return rendered
}

// arrowLabel puts the directions of a block arrow before its label, as arrows, for a drawing
// that has no arrow shape. Not in pele.
func arrowLabel(mask int, label []string) []string {
	glyphs := ""
	switch {
	case mask&(LEFT|RIGHT) == LEFT|RIGHT:
		glyphs += "↔"
	case mask&LEFT != 0:
		glyphs += "←"
	case mask&RIGHT != 0:
		glyphs += "→"
	}
	switch {
	case mask&(UP|DOWN) == UP|DOWN:
		glyphs += "↕"
	case mask&UP != 0:
		glyphs += "↑"
	case mask&DOWN != 0:
		glyphs += "↓"
	}
	if glyphs == "" {
		return label
	}
	if len(label) == 0 || js.Trim(strings.Join(label, "")) == "" {
		return []string{glyphs}
	}
	return append([]string{glyphs + " " + label[0]}, label[1:]...)
}

// geometry gives the blocks as flowchart nodes, the composites as groups, and the edges as
// routes, so that sketch/text draws a block diagram as it draws a flowchart. Not in pele.
func geometry(order []*View, routes []Route, ends [][2]*View, ox, oy, width, height float64) *types.Geometry {
	g := &types.Geometry{Width: width, Height: height}
	node := make([]int, len(order))
	for i, v := range order {
		node[i] = -1
		if i == 0 || v.kind == Space {
			continue
		}
		n := types.GeoNode{ID: v.block.id, Shape: v.shape, X: v.x, Y: v.y, W: v.bw, H: v.bh, Label: v.label.Plain(), Group: v.kind == Composite, Parent: node[v.parent]}
		if v.kind == Arrow {
			n.Shape = "block-arrow"
			n.Label = arrowLabel(v.mask, n.Label)
		}
		if v.kind == Composite {
			n.W, n.H = v.w, v.h
		}
		node[i] = len(g.Nodes)
		g.Nodes = append(g.Nodes, n)
	}
	for i, r := range routes {
		e := types.GeoEdge{From: node[ends[i][0].index], To: node[ends[i][1].index], Start: r.edge.arrowTypeStart, End: r.edge.arrowTypeEnd,
			Stroke: r.edge.thickness, Label: r.label.Plain(), LabelX: r.lx + ox, LabelY: r.ly + oy}
		if r.edge.pattern == "dotted" {
			e.Stroke = "dotted"
		}
		// The points were moved into place when the SVG was written. A leg's axis is 0 where it runs down.
		for k := 0; k < len(r.points); k += 2 {
			axis := 1.0
			if k > 0 && math.Abs(r.points[k+1]-r.points[k-1]) > math.Abs(r.points[k]-r.points[k-2]) {
				axis = 0
			}
			e.Route = append(e.Route, r.points[k], r.points[k+1], axis)
		}
		g.Edges = append(g.Edges, e)
	}
	return g
}
