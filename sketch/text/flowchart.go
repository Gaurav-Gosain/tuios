package text

import (
	"math"
	"slices"
	"sort"

	"tuios.dev/sketch"
	"tuios.dev/sketch/internal/types"
)

// rect is a box in cells, edges included.
type rect struct{ x0, y0, x1, y1 int }

func (r rect) union(o rect) rect {
	return rect{min(r.x0, o.x0), min(r.y0, o.y0), max(r.x1, o.x1), max(r.y1, o.y1)}
}

type point struct{ x, y int }

// Shapes drawn with rounded corners, and shapes drawn as a diamond.
var pointShapes = map[string]bool{"diam": true, "hex": true}

// slantShapes have slanted sides, and need a cell more on each side for
// each row past the first. cylShapes have a rim under their top edge.
var slantShapes = map[string]bool{"lean-r": true, "lean-l": true, "trap-b": true, "trap-t": true}
var cylShapes = map[string]bool{"cyl": true, "lin-cyl": true}
var dotShapes = map[string]bool{"sm-circ": true, "f-circ": true, "fr-circ": true, "cross-circ": true}

// graphKind is what a diagram type changes in the drawing of a graph of
// boxes and edges. The zero value draws a flowchart.
type graphKind struct {
	// size gives the cells a node's box needs, borders included. ok false
	// keeps the flowchart rule.
	size func(n *types.GeoNode) (w, h int, ok bool)
	// node draws a node; false leaves it to drawNode.
	node func(c *canvas, b rect, n *types.GeoNode) bool
	// head draws the marker at an edge end; false leaves it to head.
	head func(c *canvas, at point, side int, kind string) bool
	// endText is the text beside an edge end. nil gives StartText and EndText.
	endText func(e *types.GeoEdge, last bool) string
	// frame says whether to draw a group's frame and title, and with what line.
	frame func(n *types.GeoNode) (draw bool, weight uint8, title bool)
	// squeeze lets a drawing up to squeezeSpan times too wide be drawn and
	// then narrowed by dropping columns that hold only gaps and horizontal
	// lines. tight gives each box no more room than its text needs.
	squeeze, tight bool
}

func (k *graphKind) sizeOf(n *types.GeoNode) (w, h int, ok bool) {
	if k.size == nil || n.Group {
		return 0, 0, false
	}
	return k.size(n)
}

func (k *graphKind) textAt(e *types.GeoEdge, last bool) string {
	if k.endText != nil {
		return k.endText(e, last)
	}
	if last {
		return e.EndText
	}
	return e.StartText
}

func scaleOf(l *sketch.Layout, kind *graphKind) (sx, sy float64) {
	sx = 1.0 / 9
	sy = 0
	for i := range l.Nodes {
		n := &l.Nodes[i]
		if n.W <= 0 || n.H <= 0 {
			continue
		}
		// A dot or a bar takes the cells it takes, whatever the scale. The
		// flowchart keeps its rule, so that its drawings stay as they are.
		if kind.size != nil && (dotShapes[n.Shape] || n.Shape == "fork") {
			continue
		}
		dw, dh := shapeRoom(n.Shape, len(n.Label), len(n.Label)+2)
		need := float64(widest(n.Label) + 4 + dw)
		if n.Group {
			need = float64(widest(n.Label) + 6)
		}
		tall := float64(len(n.Label) + 2 + dh)
		if w, h, ok := kind.sizeOf(n); ok {
			need, tall = float64(w), float64(h)
		}
		sx = math.Max(sx, need/n.W)
		if !n.Group {
			sy = math.Max(sy, tall/n.H)
		}
	}
	// An edge with text at its ends is long enough for both texts apart:
	// across, the two side by side with room between them; down, a row each.
	for k := range l.Edges {
		e := &l.Edges[k]
		ts, te := width(kind.textAt(e, false)), width(kind.textAt(e, true))
		if ts+te == 0 || e.From == e.To || len(e.Route) < 6 {
			continue
		}
		last := len(e.Route) - 3
		dx := math.Abs(e.Route[last] - e.Route[0])
		dy := math.Abs(e.Route[last+1] - e.Route[1])
		if dx >= dy && dx > 0 {
			sx = math.Max(sx, float64(ts+te+5)/dx)
		} else if dy > 0 {
			sy = math.Max(sy, 4/dy)
		}
	}
	// A label between two boxes side by side fits the gap between them, so
	// that it covers neither box: pele leaves that room, as C4 does since
	// pele 1.9.0.
	for k := range l.Edges {
		e := &l.Edges[k]
		w := widest(e.Label)
		if w == 0 || e.From == e.To || e.From < 0 || e.To < 0 || e.From >= len(l.Nodes) || e.To >= len(l.Nodes) {
			continue
		}
		a, b := &l.Nodes[e.From], &l.Nodes[e.To]
		if a.X > b.X {
			a, b = b, a
		}
		left, right := a.X+a.W/2, b.X-b.W/2
		if a.Group || b.Group || right <= left || e.LabelX < left || e.LabelX > right ||
			math.Abs(e.LabelY-a.Y) > a.H/2 || math.Abs(e.LabelY-b.Y) > b.H/2 {
			continue
		}
		sx = math.Max(sx, float64(w+4)/(right-left))
	}
	// A cell is about twice as tall as it is wide.
	sy = math.Max(sy, sx*0.45)
	return sx, sy
}

// drawFlowchart draws a graph from its layout. With squeeze, a drawing up to
// squeezeSpan times too wide is drawn and then narrowed by dropping columns
// that hold only gaps and horizontal lines. If that is not enough, the boxes
// lose the room the layout gave them beyond their text, and it is tried again.
func drawFlowchart(l *sketch.Layout, opt Options, squeeze bool) (*canvas, error) {
	c, err := drawGraph(l, opt, &graphKind{squeeze: squeeze})
	if err == ErrTooWide && squeeze {
		return drawGraph(l, opt, &graphKind{squeeze: true, tight: true})
	}
	return c, err
}

// drawGraph draws boxes joined by edges: a flowchart, or a state, class or ER
// diagram, as kind says.
func drawGraph(l *sketch.Layout, opt Options, kind *graphKind) (*canvas, error) {
	sx, sy := scaleOf(l, kind)
	col := func(x float64) int { return int(math.Round(x * sx)) }
	row := func(y float64) int { return int(math.Round(y * sy)) }

	// Where an edge meets a node in the layout: 0 top, 1 right, 2 bottom, 3 left.
	side := func(i int, x, y float64) int {
		n := l.Nodes[i]
		dx, dy := x-n.X, y-n.Y
		hw, hh := n.W/2, n.H/2
		if math.Abs(math.Abs(dy)-hh) < 1.5 && math.Abs(dx) <= hw+1 {
			if dy < 0 {
				return 0
			}
			return 2
		}
		if math.Abs(math.Abs(dx)-hw) < 1.5 {
			if dx < 0 {
				return 3
			}
			return 1
		}
		// Nearest side by angle.
		if math.Abs(dy)*hw > math.Abs(dx)*hh {
			if dy < 0 {
				return 0
			}
			return 2
		}
		if dx < 0 {
			return 3
		}
		return 1
	}
	// The ends on each side of each node, in the order pele spread them.
	ends := map[[2]int][]graphEnd{}
	for k, e := range l.Edges {
		if e.From == e.To || len(e.Route) < 6 || e.Stroke == "invisible" {
			continue
		}
		r := e.Route
		last := len(r) - 3
		for _, f := range []struct {
			node int
			x, y float64
			last bool
		}{{e.From, r[0], r[1], false}, {e.To, r[last], r[last+1], true}} {
			s := side(f.node, f.x, f.y)
			at := f.x
			if s == 1 || s == 3 {
				at = f.y
			}
			key := [2]int{f.node, s}
			ends[key] = append(ends[key], graphEnd{k, f.last, at, width(kind.textAt(&l.Edges[k], f.last))})
		}
	}

	boxes := make([]rect, len(l.Nodes))
	for i, n := range l.Nodes {
		if n.Group {
			continue
		}
		w := max(col(n.W), widest(n.Label)+4)
		if kind.tight {
			w = widest(n.Label) + 4
		}
		// As tall as the text needs, or as the ends on a side need: a taller
		// box only spreads the drawing. A shape takes the room its outline
		// needs past that: see shapeRoom.
		_, dh := shapeRoom(n.Shape, len(n.Label), len(n.Label)+2)
		h := len(n.Label) + 2 + dh
		sized := false
		if kw, kh, ok := kind.sizeOf(&l.Nodes[i]); ok {
			w, h, sized = max(col(n.W), kw), kh, true
		}
		for _, sd := range []int{1, 3} {
			h = max(h, span(ends[[2]int{i, sd}], true)+2)
		}
		for _, sd := range []int{0, 2} {
			if list := ends[[2]int{i, sd}]; span(list, false) > len(list) {
				w = max(w, span(list, false)+2)
			}
		}
		if !sized {
			if h%2 == 0 && (pointShapes[n.Shape] || n.Shape == "odd") {
				// A point is on one middle row.
				h++
			}
			dw, _ := shapeRoom(n.Shape, len(n.Label), h)
			w = max(w, widest(n.Label)+4+dw)
		}
		if !sized && (dotShapes[n.Shape] || n.Shape == "fork") {
			w, h = 3, 1
			if n.Shape == "fork" {
				w = max(3, col(n.W))
			}
		}
		cx, cy := col(n.X), row(n.Y)
		boxes[i] = rect{cx - w/2, cy - h/2, cx - w/2 + w - 1, cy - h/2 + h - 1}
	}
	// Groups, the deepest first, around what they hold.
	depth := func(i int) int {
		d := 0
		for p := l.Nodes[i].Parent; p >= 0 && d < 64; p = l.Nodes[p].Parent {
			d++
		}
		return d
	}
	var groups []int
	for i, n := range l.Nodes {
		if n.Group {
			groups = append(groups, i)
		}
	}
	slices.SortStableFunc(groups, func(a, b int) int { return depth(b) - depth(a) })
	for _, g := range groups {
		n := l.Nodes[g]
		r := rect{col(n.X - n.W/2), row(n.Y - n.H/2), col(n.X+n.W/2) - 1, row(n.Y+n.H/2) - 1}
		for i, m := range l.Nodes {
			if m.Parent == g && i != g {
				b := boxes[i]
				r = r.union(rect{b.x0 - 2, b.y0 - 2, b.x1 + 2, b.y1 + 1})
			}
		}
		if w := widest(n.Label) + 6; r.x1-r.x0+1 < w {
			r.x1 = r.x0 + w - 1
		}
		boxes[g] = r
	}

	// The cell just outside a box on a side.
	outside := func(b rect, s int, at point) point {
		cx := min(max(at.x, b.x0+1), b.x1-1)
		cy := min(max(at.y, b.y0+1), b.y1-1)
		if b.y0 == b.y1 {
			cy = b.y0
		}
		if b.x1-b.x0 < 2 {
			cx = (b.x0 + b.x1) / 2
		}
		switch s {
		case 0:
			return point{cx, b.y0 - 1}
		case 2:
			return point{cx, b.y1 + 1}
		case 3:
			return point{b.x0 - 1, cy}
		}
		return point{b.x1 + 1, cy}
	}

	// Each end gets a cell of its own along its side, in the layout's order.
	// Edges that arrive on one side with the same head, the same stroke and
	// no text beside it share one end, so they join into one arrow instead of
	// a row of heads side by side.
	placed := map[[2]int]point{}
	for key, list := range ends {
		node, sd := key[0], key[1]
		b := boxes[node]
		var joined []int
		var joinedAt float64
		head, stroke := "", ""
		same := true
		for _, e := range list {
			if !e.last || e.text > 0 {
				continue
			}
			end, st := l.Edges[e.edge].End, l.Edges[e.edge].Stroke
			if head != "" && (end != head || st != stroke) {
				same = false
			}
			head, stroke = end, st
			joined = append(joined, e.edge)
			joinedAt += e.at
		}
		if len(joined) > 1 && same && head != "" {
			joinedAt /= float64(len(joined))
			rest := list[:0:0]
			for _, e := range list {
				if !e.last || e.text > 0 {
					rest = append(rest, e)
				}
			}
			list = append(rest, graphEnd{edge: joined[0], last: true, at: joinedAt})
		} else {
			joined = nil
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].at < list[j].at })
		lo, hi := b.x0+1, b.x1-1
		scale := col
		if sd == 1 || sd == 3 {
			lo, hi = b.y0+1, b.y1-1
			scale = row
			if pointShapes[l.Nodes[node].Shape] {
				// Edges meet a hexagon or a decision at its point.
				lo = (b.y0 + b.y1) / 2
				hi = lo
			}
		}
		prev := lo - 1
		gap := 1
		for k, e := range list {
			want := min(max(scale(e.at), lo), hi)
			// Keep order and room for the ends still to come.
			want = max(want, prev+gap)
			want = min(want, hi-room(list[k:], sd == 1 || sd == 3))
			want = max(want, lo)
			prev = want
			gap = endRoom(e.text, sd == 1 || sd == 3)
			var at point
			if sd == 1 || sd == 3 {
				at = outside(b, sd, point{b.x0, want})
			} else {
				at = outside(b, sd, point{want, b.y0})
			}
			idx := 0
			if e.last {
				idx = 1
			}
			placed[[2]int{e.edge, idx}] = at
			if e.last && len(joined) > 0 && e.edge == joined[0] {
				for _, other := range joined[1:] {
					placed[[2]int{other, 1}] = at
				}
			}
		}
	}

	type edgePath struct {
		pts        []point
		axes       []float64
		weight     uint8
		startSide  int
		endSide    int
		start, end string
		label      []string
		lx, ly     int
		self       bool
		from       int
		edge       int
		invisible  bool
	}
	var paths []edgePath
	for ei, e := range l.Edges {
		p := edgePath{start: e.Start, end: e.End, label: e.Label, lx: col(e.LabelX), ly: row(e.LabelY), from: e.From, edge: ei}
		switch e.Stroke {
		case "thick":
			p.weight = heavy
		case "dotted":
			p.weight = dotted
		case "invisible":
			p.invisible = true
		}
		if e.From == e.To || len(e.Route) < 6 {
			p.self = true
			paths = append(paths, p)
			continue
		}
		r := e.Route
		last := len(r) - 3
		p.startSide = side(e.From, r[0], r[1])
		p.endSide = side(e.To, r[last], r[last+1])
		p.pts = append(p.pts, placed[[2]int{ei, 0}])
		p.axes = append(p.axes, r[2])
		for k := 3; k < last; k += 3 {
			p.pts = append(p.pts, point{col(r[k]), row(r[k+1])})
			p.axes = append(p.axes, r[k+2])
		}
		p.pts = append(p.pts, placed[[2]int{ei, 1}])
		p.axes = append(p.axes, r[last+2])
		paths = append(paths, p)
	}

	// The canvas covers everything; shift so nothing is left of or above it.
	all := rect{0, 0, col(l.Width), row(l.Height)}
	for i, b := range boxes {
		if l.Nodes[i].W > 0 || l.Nodes[i].Group {
			all = all.union(b)
		}
	}
	for _, p := range paths {
		for _, q := range p.pts {
			all = all.union(rect{q.x, q.y, q.x, q.y})
		}
		if w := widest(p.label); w > 0 {
			all = all.union(rect{p.lx - w/2 - 1, p.ly, p.lx + w/2 + 1, p.ly + len(p.label)})
		}
		if p.self {
			b := boxes[p.from]
			all = all.union(rect{b.x0, b.y0, b.x1 + 3 + widest(p.label), b.y1})
		}
	}
	dx, dy := -all.x0, -all.y0
	cw, ch := all.x1-all.x0+1, all.y1-all.y0+2
	if opt.Cols > 0 && cw > opt.Cols && (!kind.squeeze || cw > squeezeSpan*opt.Cols) {
		return nil, ErrTooWide
	}
	c, err := newCanvas(cw, ch)
	if err != nil {
		return nil, err
	}
	move := func(r rect) rect { return rect{r.x0 + dx, r.y0 + dy, r.x1 + dx, r.y1 + dy} }

	// Group frames, the outermost first, as lines that edges cross.
	for k := len(groups) - 1; k >= 0; k-- {
		weight := uint8(light)
		if kind.frame != nil {
			draw, w, _ := kind.frame(&l.Nodes[groups[k]])
			if !draw {
				continue
			}
			weight = w
		}
		b := move(boxes[groups[k]])
		c.hline(b.x0, b.x1, b.y0, weight, roleFrame)
		c.hline(b.x0, b.x1, b.y1, weight, roleFrame)
		c.vline(b.x0, b.y0, b.y1, weight, roleFrame)
		c.vline(b.x1, b.y0, b.y1, weight, roleFrame)
	}

	// Edges. An edge from an actor of a use case diagram takes the actor's
	// series colour, so each actor's edges can be told apart where they run
	// side by side.
	actor := map[int]uint8{}
	for i, n := range l.Nodes {
		if n.Shape == "actor" {
			actor[i] = seriesRole(len(actor))
		}
	}
	toward := [4]uint8{down, left, up, right}
	outward := [4]uint8{up, right, down, left}
	for _, p := range paths {
		if p.invisible || p.self {
			continue
		}
		role := uint8(roleLine)
		if r, ok := actor[p.from]; ok {
			role = r
		}
		// The first cell joins the box it leaves. A group's frame is a line, so
		// it gets a junction where the edge leaves it.
		c.stroke(p.pts[0].x+dx, p.pts[0].y+dy, toward[p.startSide], p.weight, role)
		if l.Nodes[p.from].Group {
			in := [4]point{{0, 1}, {-1, 0}, {0, -1}, {1, 0}}[p.startSide]
			c.stroke(p.pts[0].x+dx+in.x, p.pts[0].y+dy+in.y, outward[p.startSide], light, roleFrame)
		}
		for k := 1; k < len(p.pts); k++ {
			a := point{p.pts[k-1].x + dx, p.pts[k-1].y + dy}
			b := point{p.pts[k].x + dx, p.pts[k].y + dy}
			switch {
			case a.x == b.x:
				c.vline(a.x, a.y, b.y, p.weight, role)
			case a.y == b.y:
				c.hline(a.x, b.x, a.y, p.weight, role)
			case p.axes[k] == 0:
				mid := (a.y + b.y) / 2
				if k == len(p.pts)-1 {
					mid = b.y - 1
					if a.y > b.y {
						mid = b.y + 1
					}
				}
				c.vline(a.x, a.y, mid, p.weight, role)
				c.hline(a.x, b.x, mid, p.weight, role)
				c.vline(b.x, mid, b.y, p.weight, role)
			default:
				mid := (a.x + b.x) / 2
				if k == len(p.pts)-1 {
					mid = b.x - 1
					if a.x > b.x {
						mid = b.x + 1
					}
				}
				c.hline(a.x, mid, a.y, p.weight, role)
				c.vline(mid, a.y, b.y, p.weight, role)
				c.hline(mid, b.x, b.y, p.weight, role)
			}
		}
	}

	// Nodes.
	for i, n := range l.Nodes {
		if n.Group {
			continue
		}
		if kind.node == nil || !kind.node(c, move(boxes[i]), &l.Nodes[i]) {
			drawNode(c, move(boxes[i]), n.Shape, n.Label)
		}
		if r, ok := actor[i]; ok {
			// The stick figure in the actor's colour.
			b := move(boxes[i])
			top := b.y0 + (b.y1-b.y0+1-len(n.Label))/2
			for y := top; y < top+3; y++ {
				for x := b.x0; x <= b.x1; x++ {
					if q := c.at(x, y); q != nil && q.r != 0 && q.r != ' ' {
						q.role = r
					}
				}
			}
		}
	}
	// Group titles, on the top line.
	for _, g := range groups {
		if kind.frame != nil {
			if draw, _, title := kind.frame(&l.Nodes[g]); !draw || !title {
				continue
			}
		}
		b := move(boxes[g])
		if t := l.Nodes[g].Label; len(t) > 0 && t[0] != "" {
			c.text(b.x0+2, b.y0, " "+t[0]+" ", roleTitle)
		}
	}

	// Edge labels and heads.
	for _, p := range paths {
		if p.invisible {
			continue
		}
		if p.self {
			b := move(boxes[p.from])
			y := (b.y0 + b.y1) / 2
			s := " ↻"
			if len(p.label) > 0 && p.label[0] != "" {
				s += " " + p.label[0]
			}
			c.text(b.x1+1, y, s, roleText)
			continue
		}
		for k, line := range p.label {
			w := width(line)
			if w == 0 {
				continue
			}
			x := p.lx + dx - w/2
			y := p.ly + dy - len(p.label)/2 + k
			c.put(x-1, y, ' ', roleText)
			c.text(x, y, line, roleText)
			c.put(x+w, y, ' ', roleText)
		}
		last := len(p.pts) - 1
		for _, at := range [2]struct {
			pt   point
			side int
			kind string
			last bool
		}{{p.pts[last], p.endSide, p.end, true}, {p.pts[0], p.startSide, p.start, false}} {
			q := point{at.pt.x + dx, at.pt.y + dy}
			if kind.head == nil || !kind.head(c, q, at.side, at.kind) {
				head(c, q, at.side, at.kind)
			}
			if s := kind.textAt(&l.Edges[p.edge], at.last); s != "" {
				endLabel(c, q, at.side, s)
			}
		}
	}
	if opt.Cols > 0 && c.w > opt.Cols {
		c.squeeze(c.w - opt.Cols)
		if c.w > opt.Cols {
			return nil, ErrTooWide
		}
	}
	return c, nil
}

// endRoom is the cells an end with text of width w takes along its side
// before the next end.
func endRoom(w int, upright bool) int {
	switch {
	case w == 0:
		return 1
	case upright:
		return 2
	}
	return w + 3
}

// graphEnd is where an edge meets a side of a node, in the layout.
type graphEnd struct {
	edge int
	last bool
	at   float64
	// text is the width of the text beside the end.
	text int
}

// room is the cells from the first end of a list to the last, along a side.
func room(list []graphEnd, upright bool) int {
	n := 0
	for k := 0; k+1 < len(list); k++ {
		n += endRoom(list[k].text, upright)
	}
	return n
}

// span is the cells a list of ends needs along a side, the text after the last included.
func span(list []graphEnd, upright bool) int {
	if len(list) == 0 {
		return 0
	}
	n := room(list, upright) + 1
	if t := list[len(list)-1].text; t > 0 && !upright {
		n += t + 2
	}
	return n
}

// endLabel writes the text beside an edge end: right of a line that comes in
// from above or below, above one that comes in from a side.
func endLabel(c *canvas, at point, side int, s string) {
	w := width(s)
	// Where the text may go, the first choice first: a cell of space and
	// then the text, beside the line for an end on the top or the bottom,
	// over or under it for an end on a side.
	var tries [2]point
	switch side {
	case 0, 2:
		tries = [2]point{{at.x + 2, at.y}, {at.x - 1 - w, at.y}}
	case 1:
		tries = [2]point{{at.x + 1, at.y - 1}, {at.x + 1, at.y + 1}}
	default:
		tries = [2]point{{at.x - w, at.y - 1}, {at.x - w, at.y + 1}}
	}
	for _, p := range tries {
		if c.free(p.x-1, p.x+w, p.y) {
			c.text(p.x, p.y, s, roleNote)
			return
		}
	}
	// No room on either side: the first choice, over what is there.
	c.text(tries[0].x, tries[0].y, s, roleNote)
}

// free reports whether the cells from x0 to x1 on row y hold nothing yet.
func (c *canvas) free(x0, x1, y int) bool {
	for x := x0; x <= x1; x++ {
		p := c.at(x, y)
		if p == nil {
			if x == x0 || x == x1 {
				// The margin may run off the canvas.
				continue
			}
			return false
		}
		if p.r != 0 || p.mask != 0 || p.wide {
			return false
		}
	}
	return true
}

// squeezeSpan is how many times wider than the columns a graph may be drawn
// before it is squeezed.
const squeezeSpan = 3

// head draws a marker in the cell outside a box's side, pointing into the box.
func head(c *canvas, at point, side int, kind string) {
	switch kind {
	case "arrow_point":
		c.put(at.x, at.y, [4]rune{'▼', '◄', '▲', '►'}[side], roleHead)
	case "arrow_circle":
		c.put(at.x, at.y, '●', roleHead)
	case "arrow_cross":
		c.put(at.x, at.y, '×', roleHead)
	}
}

// drawNode draws a node's box and its label, centred.
func drawNode(c *canvas, b rect, shape string, label []string) {
	// Clear what edges left inside the box.
	for y := b.y0; y <= b.y1; y++ {
		for x := b.x0; x <= b.x1; x++ {
			c.clear(x, y)
		}
	}
	switch {
	case dotShapes[shape]:
		r := '○'
		if shape == "f-circ" {
			r = '●'
		} else if shape == "fr-circ" {
			r = '◉'
		} else if shape == "cross-circ" {
			r = '⊗'
		}
		c.put((b.x0+b.x1)/2, b.y0, r, roleBox)
		return
	case shape == "fork":
		for x := b.x0; x <= b.x1; x++ {
			c.put(x, b.y0, '━', roleBox)
		}
		return
	case shape == "text", shape == "actor":
	default:
		outline(c, b, shape)
	}
	y0, h := b.y0, b.y1-b.y0+1
	if cylShapes[shape] && h >= 4 {
		// The text goes under the rim.
		y0, h = b.y0+1, h-1
	}
	top := y0 + (h-len(label))/2
	for k, line := range label {
		w := width(line)
		x := b.x0 + (b.x1-b.x0+1-w)/2
		c.text(x, top+k, line, roleText)
	}
}
