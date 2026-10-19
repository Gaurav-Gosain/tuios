// Ported from pele src/layout/compound.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package layout

import (
	"math"
	"slices"
)

type Dir string

// Direction is the direction a diagram names, or top to bottom for anything else.
func Direction(dir string) Dir {
	if dir == "BT" || dir == "LR" || dir == "RL" {
		return Dir(dir)
	}
	return "TB"
}

type CNode struct {
	W       float64
	H       float64
	Parent  int
	IsGroup bool
	// Dir is "" for none.
	Dir       Dir
	PadX      float64
	PadTop    float64
	PadBottom float64
	MinW      float64
	// Declaration order; a group takes the smallest value among its members.
	Seq int
	// How much of a side its edges may spread along, measured across the flow; 0 keeps them at its middle.
	Span float64
	X    float64
	Y    float64
}

type CEdge struct {
	Src    int
	Dst    int
	Minlen int
	LabelW float64
	LabelH float64
	// x, y, axis triples; axis is 0 where the edge runs vertically into the point, 1 horizontally.
	Route  []float64
	LabelX float64
	LabelY float64
}

type CompoundResult struct {
	Width  float64
	Height float64
}

type Side int

const (
	SideTop Side = iota
	SideRight
	SideBottom
	SideLeft
)

type portEdge struct {
	e     int
	inner int
	out   bool
	side  Side
	px    float64
	py    float64
	route []float64
}

type levelEdge struct {
	e int
	a int
	b int
}

type level struct {
	id     int
	dir    Dir
	items  []int
	edges  []levelEdge
	ports  []*portEdge
	portOf map[int]*portEdge
	routes map[int][]float64
	x      float64
	y      float64
}

const MAX_PORTS = 40000

func ShiftLayout(nodes []*CNode, edges []*CEdge, ox, oy float64) {
	for _, c := range nodes {
		c.X += ox
		c.Y += oy
	}
	for _, e := range edges {
		for k := 0; k < len(e.Route); k += 3 {
			e.Route[k] += ox
			e.Route[k+1] += oy
		}
		e.LabelX += ox
		e.LabelY += oy
	}
}

func Cnode(w, h float64, parent int) *CNode {
	return &CNode{W: w, H: h, Parent: parent}
}

func startSide(dir Dir) Side {
	switch dir {
	case "TB":
		return SideTop
	case "BT":
		return SideBottom
	case "LR":
		return SideLeft
	}
	return SideRight
}

func opposite(side Side) Side {
	return (side + 2) & 3
}

// CompoundLayout lays out a graph whose nodes may be nested in groups. Each group is laid out on its own,
// in its own direction, and then placed in its parent as a single box. An edge that crosses
// a group's border passes through a port on the side facing its other end.
func CompoundLayout(nodes []*CNode, edges []*CEdge, rootDir Dir, opt LayeredOptions) CompoundResult {
	n := len(nodes)
	levelOf := map[int]*level{}
	var order []*level
	makeLevel := func(id int, dir Dir) *level {
		l := &level{id: id, dir: dir, portOf: map[int]*portEdge{}, routes: map[int][]float64{}}
		levelOf[id] = l
		return l
	}
	root := makeLevel(-1, rootDir)
	dirOr := func(d, def Dir) Dir {
		if d == "" {
			return def
		}
		return d
	}

	children := make([][]int, n)
	var rootItems []int
	for i := 0; i < n; i++ {
		p := nodes[i].Parent
		if p >= 0 && p < n && p != i && nodes[p].IsGroup {
			children[p] = append(children[p], i)
		} else {
			nodes[i].Parent = -1
			rootItems = append(rootItems, i)
		}
	}
	root.items = rootItems
	order = append(order, root)
	depth := make([]int, n)
	placed := make([]uint8, n)
	descend := func(from int) {
		for q := from; q < len(order); q++ {
			lv := order[q]
			for _, item := range lv.items {
				placed[item] = 1
				if lv.id < 0 {
					depth[item] = 0
				} else {
					depth[item] = depth[lv.id] + 1
				}
				if nodes[item].IsGroup {
					child := makeLevel(item, dirOr(nodes[item].Dir, lv.dir))
					child.items = children[item]
					order = append(order, child)
				}
			}
		}
	}
	descend(0)
	// Groups that contain each other are unreachable from the root; lift them out.
	for i := 0; i < n; i++ {
		if placed[i] != 0 {
			continue
		}
		former := nodes[i].Parent
		children[former] = slices.DeleteFunc(slices.Clone(children[former]), func(c int) bool { return c == i })
		nodes[i].Parent = -1
		root.items = append(root.items, i)
		placed[i] = 1
		depth[i] = 0
		if nodes[i].IsGroup {
			from := len(order)
			child := makeLevel(i, dirOr(nodes[i].Dir, rootDir))
			child.items = children[i]
			order = append(order, child)
			descend(from)
		}
	}

	for q := len(order) - 1; q > 0; q-- {
		lv := order[q]
		seq := math.MaxInt
		for _, item := range lv.items {
			if nodes[item].Seq < seq {
				seq = nodes[item].Seq
			}
		}
		if seq != math.MaxInt {
			nodes[lv.id].Seq = seq
		}
	}
	for _, lv := range order {
		lv.items = slices.Clone(lv.items)
		sortBy(lv.items, func(a, b int) float64 { return or(float64(nodes[a].Seq-nodes[b].Seq), float64(a-b)) })
	}

	// Every group border an edge crosses costs a port. Past the budget, an edge is drawn between
	// the groups that hold its ends, at the level they share.
	ports := 0
	for _, e := range edges {
		a := e.Src
		b := e.Dst
		crossed := 0
		for ; depth[a] > depth[b]; crossed++ {
			a = nodes[a].Parent
		}
		for ; depth[b] > depth[a]; crossed++ {
			b = nodes[b].Parent
		}
		for ; a != b && nodes[a].Parent != nodes[b].Parent; crossed += 2 {
			a = nodes[a].Parent
			b = nodes[b].Parent
		}
		if a == b {
			continue
		}
		if ports+crossed > MAX_PORTS {
			e.Src = a
			e.Dst = b
		} else {
			ports += crossed
		}
	}

	var loops []int
	var stray []int
	for ei := 0; ei < len(edges); ei++ {
		e := edges[ei]
		e.Route = []float64{}
		if e.Src == e.Dst {
			loops = append(loops, ei)
			continue
		}
		a := e.Src
		b := e.Dst
		var aPath, bPath []int
		for depth[a] > depth[b] {
			aPath = append(aPath, a)
			a = nodes[a].Parent
		}
		for depth[b] > depth[a] {
			bPath = append(bPath, b)
			b = nodes[b].Parent
		}
		if a == b {
			stray = append(stray, ei)
			continue
		}
		for nodes[a].Parent != nodes[b].Parent {
			aPath = append(aPath, a)
			bPath = append(bPath, b)
			a = nodes[a].Parent
			b = nodes[b].Parent
		}
		lv := levelOf[nodes[a].Parent]
		lv.edges = append(lv.edges, levelEdge{e: ei, a: a, b: b})
		for pi, path := range [2][]int{aPath, bPath} {
			out := pi == 0
			for _, inner := range path {
				l := levelOf[nodes[inner].Parent]
				port := &portEdge{e: ei, inner: inner, out: out, side: SideTop}
				l.ports = append(l.ports, port)
				key := ei * 2
				if !out {
					key++
				}
				l.portOf[key] = port
			}
		}
	}

	local := make([]int, n)
	endpointBelow := func(item int, e *CEdge, out bool) bool {
		if out {
			return e.Src != item
		}
		return e.Dst != item
	}
	portKey := func(ei int, out bool) int {
		if out {
			return ei * 2
		}
		return ei*2 + 1
	}

	build := func(lv *level, sized bool) ([]*LNode, []*LEdge, []int) {
		vertical := lv.dir == "TB" || lv.dir == "BT"
		start := startSide(lv.dir)
		var lnodes []*LNode
		var ledges []*LEdge
		for i, item := range lv.items {
			local[item] = i
			node := nodes[item]
			var ln *LNode
			if sized {
				if vertical {
					ln = Lnode(node.W, node.H, KindNode)
				} else {
					ln = Lnode(node.H, node.W, KindNode)
				}
			} else {
				ln = Lnode(0, 0, KindNode)
			}
			if node.IsGroup {
				ln.Span = math.Min(node.Span, ln.W-2*node.PadX)
			} else {
				ln.Span = node.Span
			}
			lnodes = append(lnodes, ln)
		}
		held := func(item, ei int, out bool) bool { return nodes[item].IsGroup && endpointBelow(item, edges[ei], out) }
		offset := func(item, ei int, out bool) float64 {
			if !sized || !nodes[item].IsGroup || !endpointBelow(item, edges[ei], out) {
				return 0
			}
			port := levelOf[item].portOf[portKey(ei, out)]
			if vertical {
				return port.px - nodes[item].W/2
			}
			return port.py - nodes[item].H/2
		}
		for _, le := range lv.edges {
			e := edges[le.e]
			edge := Ledge(local[le.a], local[le.b], e.Minlen, 0, 0)
			if sized {
				if vertical {
					edge.LabelW, edge.LabelH = e.LabelW, e.LabelH
				} else {
					edge.LabelW, edge.LabelH = e.LabelH, e.LabelW
				}
				edge.TailDx = offset(le.a, le.e, true)
				edge.HeadDx = offset(le.b, le.e, false)
				edge.TailFixed = held(le.a, le.e, true)
				edge.HeadFixed = held(le.b, le.e, false)
			}
			ledges = append(ledges, edge)
		}
		dummies := fillInt(len(lv.ports), -1)
		for i, port := range lv.ports {
			if port.side == start || port.side == opposite(start) {
				d := len(lnodes)
				kind := KindEndPort
				if port.side == start {
					kind = KindStartPort
				}
				lnodes = append(lnodes, Lnode(0, 0, kind))
				dummies[i] = d
				var edge *LEdge
				if port.out {
					edge = Ledge(local[port.inner], d, 1, 0, 0)
					edge.TailDx = offset(port.inner, port.e, true)
					edge.TailFixed = held(port.inner, port.e, true)
				} else {
					edge = Ledge(d, local[port.inner], 1, 0, 0)
					edge.HeadDx = offset(port.inner, port.e, false)
					edge.HeadFixed = held(port.inner, port.e, false)
				}
				ledges = append(ledges, edge)
			} else {
				if port.side == SideTop || port.side == SideLeft {
					lnodes[local[port.inner]].Pin = -1
				} else {
					lnodes[local[port.inner]].Pin = 1
				}
			}
		}
		return lnodes, ledges, dummies
	}

	// Top-down: rank each level to learn which side of a group every crossing edge leaves from.
	for _, lv := range order {
		for _, port := range lv.ports {
			if nodes[port.inner].IsGroup && endpointBelow(port.inner, edges[port.e], port.out) {
				levelOf[port.inner].portOf[portKey(port.e, port.out)].side = port.side
			}
		}
		if len(lv.edges) == 0 {
			continue
		}
		needed := false
		for _, le := range lv.edges {
			e := edges[le.e]
			if (nodes[le.a].IsGroup && e.Src != le.a) || (nodes[le.b].IsGroup && e.Dst != le.b) {
				needed = true
			}
		}
		if !needed {
			continue
		}
		lnodes, ledges, _ := build(lv, false)
		Orient(lnodes, ledges)
		Rank(lnodes, ledges, 1)
		start := startSide(lv.dir)
		for i, le := range lv.edges {
			e := edges[le.e]
			aAbove := !ledges[i].Reversed
			if nodes[le.a].IsGroup && e.Src != le.a {
				side := start
				if aAbove {
					side = opposite(start)
				}
				levelOf[le.a].portOf[le.e*2].side = side
			}
			if nodes[le.b].IsGroup && e.Dst != le.b {
				side := opposite(start)
				if aAbove {
					side = start
				}
				levelOf[le.b].portOf[le.e*2+1].side = side
			}
		}
	}

	// Bottom-up: lay out each level now that the sizes of its groups are known.
	total := CompoundResult{}
	for q := len(order) - 1; q >= 0; q-- {
		lv := order[q]
		var group *CNode
		if lv.id >= 0 {
			group = nodes[lv.id]
		}
		vertical := lv.dir == "TB" || lv.dir == "BT"
		flip := lv.dir == "BT" || lv.dir == "RL"
		lnodes, ledges, dummies := build(lv, true)
		size := Layered(lnodes, ledges, opt)
		contentW, contentH := size.Height, size.Width
		if vertical {
			contentW, contentH = size.Width, size.Height
		}
		padX, padTop, padBottom, minW := 0.0, 0.0, 0.0, 0.0
		if group != nil {
			padX, padTop, padBottom, minW = group.PadX, group.PadTop, group.PadBottom, group.MinW
		}
		boxW := math.Max(contentW+2*padX, minW)
		boxH := contentH + padTop + padBottom
		offX := (boxW - contentW) / 2
		offY := padTop
		tx := func(lx, ly float64) float64 {
			if vertical {
				return offX + lx
			}
			if flip {
				return offX + (size.Height - ly)
			}
			return offX + ly
		}
		ty := func(lx, ly float64) float64 {
			if vertical {
				if flip {
					return offY + (size.Height - ly)
				}
				return offY + ly
			}
			return offY + lx
		}
		axis := 1.0
		if vertical {
			axis = 0
		}
		convert := func(points []float64) []float64 {
			out := make([]float64, 0, len(points)/2*3)
			for i := 0; i < len(points); i += 2 {
				out = append(out, tx(points[i], points[i+1]), ty(points[i], points[i+1]), axis)
			}
			return out
		}

		for i, item := range lv.items {
			nodes[item].X = tx(lnodes[i].X, lnodes[i].Y)
			nodes[item].Y = ty(lnodes[i].X, lnodes[i].Y)
		}
		for i, le := range lv.edges {
			edge := ledges[i]
			lv.routes[le.e] = convert(edge.Points)
			e := edges[le.e]
			if e.LabelW > 0 {
				e.LabelX = tx(edge.LabelX, edge.LabelY)
				e.LabelY = ty(edge.LabelX, edge.LabelY)
			}
		}
		k := len(lv.edges)
		for i, port := range lv.ports {
			item := nodes[port.inner]
			if dummies[i] >= 0 {
				route := convert(ledges[k].Points)
				k++
				at := 0
				if port.out {
					at = len(route) - 3
				}
				bx := route[at]
				by := route[at+1]
				switch port.side {
				case SideTop:
					by = 0
				case SideBottom:
					by = boxH
				case SideLeft:
					bx = 0
				default:
					bx = boxW
				}
				if port.out {
					route = append(route, bx, by, axis)
				} else {
					route = append([]float64{bx, by, axis}, route...)
				}
				port.px = bx
				port.py = by
				port.route = route
				continue
			}
			ix := item.X
			iy := item.Y
			if item.IsGroup && endpointBelow(port.inner, edges[port.e], port.out) {
				inner := levelOf[port.inner].portOf[portKey(port.e, port.out)]
				ix += inner.px - item.W/2
				iy += inner.py - item.H/2
			} else if port.side == SideTop {
				iy -= item.H / 2
			} else if port.side == SideBottom {
				iy += item.H / 2
			} else if port.side == SideLeft {
				ix -= item.W / 2
			} else {
				ix += item.W / 2
			}
			bx := ix
			by := iy
			switch port.side {
			case SideTop:
				by = 0
			case SideBottom:
				by = boxH
			case SideLeft:
				bx = 0
			default:
				bx = boxW
			}
			a := 1.0
			if port.side == SideTop || port.side == SideBottom {
				a = 0
			}
			port.px = bx
			port.py = by
			if port.out {
				port.route = []float64{ix, iy, a, bx, by, a}
			} else {
				port.route = []float64{bx, by, a, ix, iy, a}
			}
		}

		if group != nil {
			group.W = boxW
			group.H = boxH
		} else {
			total = CompoundResult{Width: boxW, Height: boxH}
		}
	}

	// Top-down again: turn positions inside each box into diagram coordinates.
	for _, lv := range order {
		ox := lv.x
		oy := lv.y
		for _, item := range lv.items {
			node := nodes[item]
			node.X += ox
			node.Y += oy
			if node.IsGroup {
				child := levelOf[item]
				child.x = node.X - node.W/2
				child.y = node.Y - node.H/2
			}
		}
	}

	appendRoute := func(route []float64, part []float64, ox, oy float64) []float64 {
		for i := 0; i < len(part); i += 3 {
			x := part[i] + ox
			y := part[i+1] + oy
			l := len(route)
			if l >= 3 && math.Abs(route[l-3]-x) < 0.01 && math.Abs(route[l-2]-y) < 0.01 {
				continue
			}
			route = append(route, x, y, part[i+2])
		}
		return route
	}

	for _, lv := range order {
		for _, le := range lv.edges {
			e := edges[le.e]
			route := []float64{}
			var outward []*level
			for item := e.Src; nodes[item].Parent != lv.id; item = nodes[item].Parent {
				outward = append(outward, levelOf[nodes[item].Parent])
			}
			for _, l := range outward {
				route = appendRoute(route, l.portOf[le.e*2].route, l.x, l.y)
			}
			route = appendRoute(route, lv.routes[le.e], lv.x, lv.y)
			var inward []*level
			for item := e.Dst; nodes[item].Parent != lv.id; item = nodes[item].Parent {
				inward = append(inward, levelOf[nodes[item].Parent])
			}
			for i := len(inward) - 1; i >= 0; i-- {
				route = appendRoute(route, inward[i].portOf[le.e*2+1].route, inward[i].x, inward[i].y)
			}
			e.Route = route
			if e.LabelW > 0 {
				e.LabelX += lv.x
				e.LabelY += lv.y
			}
		}
	}

	for _, ei := range stray {
		e := edges[ei]
		a := nodes[e.Src]
		b := nodes[e.Dst]
		e.Route = []float64{a.X, a.Y, 0, b.X, b.Y, 0}
		e.LabelX = (a.X + b.X) / 2
		e.LabelY = (a.Y + b.Y) / 2
	}
	for _, ei := range loops {
		e := edges[ei]
		node := nodes[e.Src]
		e.LabelX = node.X + node.W/2 + 28 + e.LabelW/2
		e.LabelY = node.Y
	}

	return total
}
