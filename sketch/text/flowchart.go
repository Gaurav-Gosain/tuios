package text

import (
	"math"
	"slices"

	"tuios.dev/sketch"
)

// rect is a box in cells, edges included.
type rect struct{ x0, y0, x1, y1 int }

func (r rect) union(o rect) rect {
	return rect{min(r.x0, o.x0), min(r.y0, o.y0), max(r.x1, o.x1), max(r.y1, o.y1)}
}

type point struct{ x, y int }

// Shapes drawn with rounded corners, and shapes drawn as a diamond.
var roundShapes = map[string]bool{"rounded": true, "stadium": true, "circle": true, "dbl-circ": true, "ellipse": true, "cyl": true, "lin-cyl": true, "h-cyl": true, "delay": true, "cloud": true}
var pointShapes = map[string]bool{"diam": true, "hex": true}
var dotShapes = map[string]bool{"sm-circ": true, "f-circ": true, "fr-circ": true, "cross-circ": true}

func scaleOf(l *sketch.Layout) (sx, sy float64) {
	sx = 1.0 / 9
	sy = 0
	for _, n := range l.Nodes {
		if n.W <= 0 || n.H <= 0 {
			continue
		}
		need := float64(widest(n.Label) + 4)
		if pointShapes[n.Shape] {
			need += 2
		}
		if n.Group {
			need = float64(widest(n.Label) + 6)
		}
		sx = math.Max(sx, need/n.W)
		if !n.Group {
			sy = math.Max(sy, float64(len(n.Label)+2)/n.H)
		}
	}
	// A cell is about twice as tall as it is wide.
	sy = math.Max(sy, sx*0.45)
	return sx, sy
}

func drawFlowchart(l *sketch.Layout, opt Options) (*canvas, error) {
	sx, sy := scaleOf(l)
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
	type end struct {
		edge int
		last bool
		at   float64
	}
	ends := map[[2]int][]end{}
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
			ends[key] = append(ends[key], end{k, f.last, at})
		}
	}

	boxes := make([]rect, len(l.Nodes))
	for i, n := range l.Nodes {
		if n.Group {
			continue
		}
		w := max(col(n.W), widest(n.Label)+4)
		if pointShapes[n.Shape] {
			w = max(w, widest(n.Label)+6)
		}
		// As tall as the text needs, or as the ends on a side need: a taller
		// box only spreads the drawing.
		h := len(n.Label) + 2
		for _, sd := range []int{1, 3} {
			h = max(h, len(ends[[2]int{i, sd}])+2)
		}
		if dotShapes[n.Shape] || n.Shape == "fork" {
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
	placed := map[[2]int]point{}
	for key, list := range ends {
		node, sd := key[0], key[1]
		b := boxes[node]
		slices.SortStableFunc(list, func(a, b end) int {
			switch {
			case a.at < b.at:
				return -1
			case a.at > b.at:
				return 1
			}
			return 0
		})
		lo, hi := b.x0+1, b.x1-1
		scale := col
		if sd == 1 || sd == 3 {
			lo, hi = b.y0+1, b.y1-1
			scale = row
		}
		prev := lo - 1
		for k, e := range list {
			want := min(max(scale(e.at), lo), hi)
			// Keep order and room for the ends still to come.
			want = max(want, prev+1)
			want = min(want, hi-(len(list)-1-k))
			want = max(want, lo)
			prev = want
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
		invisible  bool
	}
	var paths []edgePath
	for ei, e := range l.Edges {
		p := edgePath{start: e.Start, end: e.End, label: e.Label, lx: col(e.LabelX), ly: row(e.LabelY), from: e.From}
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
	if opt.Cols > 0 && cw > opt.Cols {
		return nil, ErrTooWide
	}
	c, err := newCanvas(cw, ch)
	if err != nil {
		return nil, err
	}
	move := func(r rect) rect { return rect{r.x0 + dx, r.y0 + dy, r.x1 + dx, r.y1 + dy} }

	// Group frames, the outermost first, as lines that edges cross.
	for k := len(groups) - 1; k >= 0; k-- {
		b := move(boxes[groups[k]])
		c.hline(b.x0, b.x1, b.y0, light, roleFrame)
		c.hline(b.x0, b.x1, b.y1, light, roleFrame)
		c.vline(b.x0, b.y0, b.y1, light, roleFrame)
		c.vline(b.x1, b.y0, b.y1, light, roleFrame)
	}

	// Edges.
	toward := [4]uint8{down, left, up, right}
	for _, p := range paths {
		if p.invisible || p.self {
			continue
		}
		// The first cell joins the box it leaves.
		c.stroke(p.pts[0].x+dx, p.pts[0].y+dy, toward[p.startSide], p.weight, roleLine)
		for k := 1; k < len(p.pts); k++ {
			a := point{p.pts[k-1].x + dx, p.pts[k-1].y + dy}
			b := point{p.pts[k].x + dx, p.pts[k].y + dy}
			switch {
			case a.x == b.x:
				c.vline(a.x, a.y, b.y, p.weight, roleLine)
			case a.y == b.y:
				c.hline(a.x, b.x, a.y, p.weight, roleLine)
			case p.axes[k] == 0:
				mid := (a.y + b.y) / 2
				if k == len(p.pts)-1 {
					mid = b.y - 1
					if a.y > b.y {
						mid = b.y + 1
					}
				}
				c.vline(a.x, a.y, mid, p.weight, roleLine)
				c.hline(a.x, b.x, mid, p.weight, roleLine)
				c.vline(b.x, mid, b.y, p.weight, roleLine)
			default:
				mid := (a.x + b.x) / 2
				if k == len(p.pts)-1 {
					mid = b.x - 1
					if a.x > b.x {
						mid = b.x + 1
					}
				}
				c.hline(a.x, mid, a.y, p.weight, roleLine)
				c.vline(mid, a.y, b.y, p.weight, roleLine)
				c.hline(mid, b.x, b.y, p.weight, roleLine)
			}
		}
	}

	// Nodes.
	for i, n := range l.Nodes {
		if n.Group {
			continue
		}
		drawNode(c, move(boxes[i]), n.Shape, n.Label)
	}
	// Group titles, on the top line.
	for _, g := range groups {
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
		head(c, point{p.pts[last].x + dx, p.pts[last].y + dy}, p.endSide, p.end)
		head(c, point{p.pts[0].x + dx, p.pts[0].y + dy}, p.startSide, p.start)
	}
	return c, nil
}

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
	case shape == "text":
	default:
		tl, tr, bl, br := '┌', '┐', '└', '┘'
		sideL, sideR := '│', '│'
		if roundShapes[shape] {
			tl, tr, bl, br = '╭', '╮', '╰', '╯'
		}
		if pointShapes[shape] {
			tl, tr, bl, br = '╱', '╲', '╲', '╱'
		}
		for x := b.x0 + 1; x < b.x1; x++ {
			c.put(x, b.y0, '─', roleBox)
			c.put(x, b.y1, '─', roleBox)
		}
		for y := b.y0 + 1; y < b.y1; y++ {
			l, r := sideL, sideR
			if pointShapes[shape] && y == (b.y0+b.y1)/2 {
				l, r = '<', '>'
			}
			c.put(b.x0, y, l, roleBox)
			c.put(b.x1, y, r, roleBox)
		}
		c.put(b.x0, b.y0, tl, roleBox)
		c.put(b.x1, b.y0, tr, roleBox)
		c.put(b.x0, b.y1, bl, roleBox)
		c.put(b.x1, b.y1, br, roleBox)
	}
	h := b.y1 - b.y0 + 1
	top := b.y0 + (h-len(label))/2
	for k, line := range label {
		w := width(line)
		x := b.x0 + (b.x1-b.x0+1-w)/2
		c.text(x, top+k, line, roleText)
	}
}
