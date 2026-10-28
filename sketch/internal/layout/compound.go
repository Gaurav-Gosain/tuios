// Ported from pele src/layout/compound.ts at 8aff183be57021b533f319a29341a529be934b08.

package layout

import (
	"math"
	"slices"

	"tuios.dev/sketch/internal/js"
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
	// Cross-flow position outside the group from a previous layout; NaN if unknown.
	hint  float64
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

// Limit repeated layouts to small graphs.
const (
	MAX_ORDERED_PORTS = 400
	MAX_ORDERED_EDGES = 500
)

// Crossing tolerance before trying independent group ordering.
const (
	FLAT_SLACK = 1.5
	FLAT_SPARE = 4
)

type snapshotOf struct {
	nodes  []float64
	routes [][]float64
	labels []float64
	levels []float64
	total  CompoundResult
}

func snapshot(nodes []*CNode, edges []*CEdge, levels []*level, total CompoundResult) *snapshotOf {
	at := make([]float64, 0, 4*len(nodes))
	for _, c := range nodes {
		at = append(at, c.X, c.Y, c.W, c.H)
	}
	labels := make([]float64, 0, 2*len(edges))
	for _, e := range edges {
		labels = append(labels, e.LabelX, e.LabelY)
	}
	var places []float64
	for _, lv := range levels {
		places = append(places, lv.x, lv.y)
		for _, port := range lv.ports {
			places = append(places, port.px, port.py)
		}
	}
	routes := make([][]float64, len(edges))
	for i, e := range edges {
		routes[i] = e.Route
	}
	return &snapshotOf{nodes: at, routes: routes, labels: labels, levels: places, total: total}
}

func restore(s *snapshotOf, nodes []*CNode, edges []*CEdge, levels []*level) CompoundResult {
	for i, c := range nodes {
		c.X = s.nodes[4*i]
		c.Y = s.nodes[4*i+1]
		c.W = s.nodes[4*i+2]
		c.H = s.nodes[4*i+3]
	}
	for i, e := range edges {
		e.Route = s.routes[i]
		e.LabelX = s.labels[2*i]
		e.LabelY = s.labels[2*i+1]
	}
	k := 0
	for _, lv := range levels {
		lv.x = s.levels[k]
		lv.y = s.levels[k+1]
		k += 2
		for _, port := range lv.ports {
			port.px = s.levels[k]
			port.py = s.levels[k+1]
			k += 2
		}
	}
	return s.total
}

// Ignore crossings within this distance of a shared node.
const FAN = 24

// Tolerance for collinear points: IN_LINE, which layered.go declares with the same value.

const (
	CURVE_PIECES  = 3
	CORNER_PIECES = 2
)

// Allow for a step, an S-curve, or a straight segment plus a rounded corner.
const STEP_PIECES = max(3, CURVE_PIECES, CORNER_PIECES+1)

// Match the renderer's corner handles.
const HANDLE = 0.62

const MAX_INSERT = 128

type Style int

const (
	StyleCurved Style = iota
	StyleStepped
	StyleLinear
)

func StyleOf(opt LayeredOptions) Style {
	if opt.Tracks {
		return StyleStepped
	}
	if opt.Straight {
		return StyleLinear
	}
	return StyleCurved
}

func RouteCrossings(nodes []*CNode, edges []*CEdge, style Style) int {
	size := 0
	for _, e := range edges {
		if e.Src != e.Dst && len(e.Route) > 3 {
			size += (len(e.Route)/3 - 1) * STEP_PIECES
		}
	}
	ax := make([]float64, size)
	ay := make([]float64, size)
	bx := make([]float64, size)
	by := make([]float64, size)
	low := make([]float64, size)
	high := make([]float64, size)
	owner := make([]int, size)
	// Endpoint flags: 1 for the first segment, 2 for the last.
	ends := make([]uint8, size)
	count := 0
	top := inf
	bottom := negInf
	fromX := math.NaN()
	fromY := math.NaN()
	toX := math.NaN()
	toY := math.NaN()
	add := func(ei int, part uint8, x0, y0, x1, y1 float64) {
		p := count - 1
		on := x0 == toX && y0 == toY && ((y1 == y0 && fromY == y0) || (x1 == x0 && fromX == x0))
		if on && p >= 0 && owner[p] == ei && ends[p] == part && float64((x0-fromX)*(x1-x0))+float64((y0-fromY)*(y1-y0)) > 0 {
			count--
			x0 = fromX
			y0 = fromY
		}
		fromX = x0
		fromY = y0
		toX = x1
		toY = y1
		ends[count] = part
		ax[count] = math.Min(x0, x1)
		bx[count] = math.Max(x0, x1)
		if x0 <= x1 {
			ay[count] = y0
			by[count] = y1
		} else {
			ay[count] = y1
			by[count] = y0
		}
		low[count] = math.Min(y0, y1)
		high[count] = math.Max(y0, y1)
		if low[count] < top {
			top = low[count]
		}
		if high[count] > bottom {
			bottom = high[count]
		}
		owner[count] = ei
		count++
	}
	for ei := 0; ei < len(edges); ei++ {
		e := edges[ei]
		if e.Src == e.Dst {
			continue
		}
		r := e.Route
		aligned := func(k int) bool { return math.Abs(r[k]-r[k-3]) < 0.01 || math.Abs(r[k+1]-r[k-2]) < 0.01 }
		length := func(k int) float64 { return math.Abs(r[k]-r[k-3]) + math.Abs(r[k+1]-r[k-2]) }
		turns := func(k int) bool {
			return style == StyleCurved &&
				k > 0 &&
				k < len(r)-3 &&
				aligned(k) &&
				aligned(k+3) &&
				length(k) >= 0.01 &&
				length(k+3) >= 0.01 &&
				(math.Abs(r[k]-r[k-3]) < 0.01) != (math.Abs(r[k+3]-r[k]) < 0.01)
		}
		// An edge to its own group has no route yet: pele reads undefined and adds nothing.
		if len(r) < 2 {
			continue
		}
		atX := r[0]
		atY := r[1]
		for k := 3; k < len(r); k += 3 {
			var part uint8
			if k == 3 {
				part |= 1
			}
			if k == len(r)-3 {
				part |= 2
			}
			x0 := atX
			y0 := atY
			x1 := r[k]
			y1 := r[k+1]
			atX = x1
			atY = y1
			if aligned(k) || style == StyleLinear {
				if !turns(k) {
					add(ei, part, x0, y0, x1, y1)
					continue
				}
				// Adjacent corners share a leg, so each gets half.
				before := length(k)
				if turns(k - 3) {
					before = length(k) / 2
				}
				after := length(k + 3)
				if turns(k + 3) {
					after = length(k+3) / 2
				}
				ux := (x1 - r[k-3]) / length(k)
				uy := (y1 - r[k-2]) / length(k)
				vx := (r[k+3] - x1) / length(k+3)
				vy := (r[k+4] - y1) / length(k+3)
				sx := x1 - float64(ux*before)
				sy := y1 - float64(uy*before)
				add(ei, part, x0, y0, sx, sy)
				c1x := x1 - float64(float64(ux*before)*(1-HANDLE))
				c1y := y1 - float64(float64(uy*before)*(1-HANDLE))
				c2x := x1 + float64(float64(vx*after)*(1-HANDLE))
				c2y := y1 + float64(float64(vy*after)*(1-HANDLE))
				atX = x1 + float64(vx*after)
				atY = y1 + float64(vy*after)
				turn := part
				if k+3 == len(r)-3 {
					turn |= 2
				}
				px := sx
				py := sy
				for i := 1; i <= CORNER_PIECES; i++ {
					t := float64(i) / CORNER_PIECES
					u := 1 - t
					x := float64(u*u*u*sx) + float64(3*u*u*t*c1x) + float64(3*u*t*t*c2x) + float64(t*t*t*atX)
					y := float64(u*u*u*sy) + float64(3*u*u*t*c1y) + float64(3*u*t*t*c2y) + float64(t*t*t*atY)
					add(ei, turn, px, py, x, y)
					px = x
					py = y
				}
				continue
			}
			if style == StyleStepped {
				down := r[k+2] == 0
				mx, my := (x0+x1)/2, y0
				if down {
					mx, my = x0, (y0+y1)/2
				}
				nx, ny := mx, y1
				if down {
					nx, ny = x1, my
				}
				add(ei, part, x0, y0, mx, my)
				add(ei, part, mx, my, nx, ny)
				add(ei, part, nx, ny, x1, y1)
				continue
			}
			// Approximate the rendered S-curve with straight segments.
			down := r[k+2] == 0
			var c float64
			if down {
				c = Ease(x1-x0, y1-y0)
			} else {
				c = Ease(y1-y0, x1-x0)
			}
			px := x0
			py := y0
			for i := 1; i <= CURVE_PIECES; i++ {
				t := float64(i) / CURVE_PIECES
				u := 1 - t
				across := t * t * (3 - float64(2*t))
				along := float64(3*c*t*u*u) + float64(3*(1-c)*t*t*u) + float64(t*t*t)
				fx, fy := along, across
				if down {
					fx, fy = across, along
				}
				x := x0 + float64((x1-x0)*fx)
				y := y0 + float64((y1-y0)*fy)
				add(ei, part, px, py, x, y)
				px = x
				py = y
			}
		}
	}
	// Count each pair only in the first band shared by both segments.
	bands := 1
	scale := 0.0
	if bottom > top {
		bands = int(js.Round(math.Sqrt(float64(count))))
		scale = float64(bands) / (bottom - top)
	}
	bandOf := func(y float64) int { return min(bands-1, int(math.Floor((y-top)*scale))) }
	first := make([]int, count)
	bandStart := make([]int, bands+1)
	for p := 0; p < count; p++ {
		first[p] = bandOf(low[p])
		for b, last := first[p], bandOf(high[p]); b <= last; b++ {
			bandStart[b+1]++
		}
	}
	for b := 0; b < bands; b++ {
		bandStart[b+1] += bandStart[b]
	}
	fill := slices.Clone(bandStart[:bands])
	inBand := make([]int, bandStart[bands])
	for p := 0; p < count; p++ {
		for b, last := first[p], bandOf(high[p]); b <= last; b++ {
			inBand[fill[b]] = p
			fill[b]++
		}
	}
	// Edges merging into a shared endpoint run do not count as crossings.
	runs := make([]float64, len(edges)*8)
	inLine := func(r []float64, a, b, c int) bool {
		return (math.Abs(r[a]-r[b]) < IN_LINE && math.Abs(r[a]-r[c]) < IN_LINE) || (math.Abs(r[a+1]-r[b+1]) < IN_LINE && math.Abs(r[a+1]-r[c+1]) < IN_LINE)
	}
	for ei := 0; ei < len(edges); ei++ {
		r := edges[ei].Route
		if len(r) < 6 {
			continue
		}
		k := 3
		for k+3 < len(r) && inLine(r, 0, k, k+3) {
			k += 3
		}
		last := len(r) - 3
		j := last - 3
		for j >= 3 && inLine(r, last, j, j-3) {
			j -= 3
		}
		copy(runs[ei*8:], []float64{r[0], r[1], r[k], r[k+1], r[j], r[j+1], r[last], r[last+1]})
	}
	onRun := func(ei int, end uint8, x, y float64) bool {
		o := ei * 8
		if end != 1 {
			o += 4
		}
		x0 := runs[o]
		y0 := runs[o+1]
		dx := runs[o+2] - x0
		dy := runs[o+3] - y0
		length := float64(dx*dx) + float64(dy*dy)
		t := 0.0
		if length > 0 {
			t = math.Max(0, math.Min(1, (float64((x-x0)*dx)+float64((y-y0)*dy))/length))
		}
		return js.Hypot(x-x0-float64(t*dx), y-y0-float64(t*dy)) < IN_LINE
	}
	total := 0
	for band := 0; band < bands; band++ {
		bandEnd := bandStart[band+1]
		if bandEnd-bandStart[band] > MAX_INSERT {
			sortBy(inBand[bandStart[band]:bandEnd], func(p, q int) float64 { return or(ax[p]-ax[q], float64(p-q)) })
		} else {
			for i := bandStart[band] + 1; i < bandEnd; i++ {
				p := inBand[i]
				j := i
				for ; j > bandStart[band] && ax[inBand[j-1]] > ax[p]; j-- {
					inBand[j] = inBand[j-1]
				}
				inBand[j] = p
			}
		}
		for s := bandStart[band]; s < bandEnd; s++ {
			p := inBand[s]
			dx := bx[p] - ax[p]
			dy := by[p] - ay[p]
			here := first[p] == band
			for t := s + 1; t < bandEnd; t++ {
				q := inBand[t]
				if ax[q] > bx[p] {
					break
				}
				if high[q] < low[p] || low[q] > high[p] || owner[p] == owner[q] || (!here && first[q] != band) {
					continue
				}
				d1 := float64(dx*(ay[q]-ay[p])) - float64(dy*(ax[q]-ax[p]))
				d2 := float64(dx*(by[q]-ay[p])) - float64(dy*(bx[q]-ax[p]))
				// Treat points on the line as one side to count segment-boundary crossings once.
				if (d1 > 0) == (d2 > 0) {
					continue
				}
				ex := bx[q] - ax[q]
				ey := by[q] - ay[q]
				d3 := float64(ex*(ay[p]-ay[q])) - float64(ey*(ax[p]-ax[q]))
				d4 := float64(ex*(by[p]-ay[q])) - float64(ey*(bx[p]-ax[q]))
				if (d3 > 0) == (d4 > 0) {
					continue
				}
				ep := edges[owner[p]]
				eq := edges[owner[q]]
				at := d3 / (d3 - d4)
				x := ax[p] + float64(at*dx)
				y := ay[p] + float64(at*dy)
				// Exclude fan-in and fan-out at shared nodes.
				fan := func(v int, pe, qe uint8) bool {
					return ((ends[p]&pe) != 0 && (ends[q]&qe) != 0) ||
						(math.Abs(x-nodes[v].X) < nodes[v].W/2+FAN && math.Abs(y-nodes[v].Y) < nodes[v].H/2+FAN) ||
						(onRun(owner[p], pe, x, y) && onRun(owner[q], qe, x, y))
				}
				if ep.Src == eq.Src && fan(ep.Src, 1, 1) {
					continue
				}
				if ep.Src == eq.Dst && fan(ep.Src, 1, 2) {
					continue
				}
				if ep.Dst == eq.Src && fan(ep.Dst, 2, 1) {
					continue
				}
				if ep.Dst == eq.Dst && fan(ep.Dst, 2, 2) {
					continue
				}
				total++
			}
		}
	}
	return total
}

// InheritCrossedDirections matches Mermaid: groups crossed by edges inherit their parent's
// direction. `end` gives the node indices of edge i, -1 for none, for n edges.
func InheritCrossedDirections(nodes []*CNode, n int, end func(i int) (src, dst int)) {
	// Only a group with a direction of its own can lose it; most diagrams have none.
	directed := false
	for _, c := range nodes {
		if c.Dir != "" {
			directed = true
			break
		}
	}
	if !directed {
		return
	}
	// inside reports whether g is an ancestor of node i.
	inside := func(i, g int) bool {
		for p, steps := nodes[i].Parent, 0; p >= 0 && steps <= len(nodes); p, steps = nodes[p].Parent, steps+1 {
			if p == g {
				return true
			}
		}
		return false
	}
	for i := range n {
		src, dst := end(i)
		if src < 0 || dst < 0 || src == dst {
			continue
		}
		for _, pair := range [2][2]int{{src, dst}, {dst, src}} {
			for p, steps := nodes[pair[0]].Parent, 0; p >= 0 && steps <= len(nodes); p, steps = nodes[p].Parent, steps+1 {
				if !inside(pair[1], p) {
					nodes[p].Dir = ""
				}
			}
		}
	}
}

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
				port := &portEdge{e: ei, inner: inner, out: out, side: SideTop, hint: math.NaN()}
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

	// Set below, before build is first called: whether nodes take their keys from the flat
	// layout, the keys, and the hint for a port on the far side of a group.
	key := make([]float64, n)
	for i := range key {
		key[i] = math.NaN()
	}
	keyed := false
	var farHint func(lv *level, port *portEdge) float64

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
				ln.Span = math.Min(node.Span, ln.W-float64(2*node.PadX))
			} else {
				ln.Span = node.Span
			}
			if keyed {
				ln.Key = key[item]
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
				ln := Lnode(0, 0, kind)
				if keyed {
					if port.out {
						ln.Key = key[edges[port.e].Dst]
					} else {
						ln.Key = key[edges[port.e].Src]
					}
				}
				if math.IsNaN(port.hint) && keyed && farHint != nil {
					ln.Hint = farHint(lv, port)
				} else {
					ln.Hint = port.hint
				}
				lnodes = append(lnodes, ln)
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

	ordered := ports > 1 && ports <= MAX_ORDERED_PORTS && len(edges) <= MAX_ORDERED_EDGES

	// Use a flat layout to coordinate node and port order across groups.
	flatCrossings := 0
	flat := len(order) > 1 && ordered
	for _, lv := range order {
		if lv.dir != rootDir {
			flat = false
		}
	}
	if flat {
		vertical := rootDir == "TB" || rootDir == "BT"
		leaf := fillInt(n, -1)
		group := fillInt(n, -1)
		var lnodes []*LNode
		var of []int
		var parent []int
		for i := 0; i < n; i++ {
			if !nodes[i].IsGroup {
				continue
			}
			group[i] = len(parent)
			parent = append(parent, -1)
		}
		for i := 0; i < n; i++ {
			p := nodes[i].Parent
			if nodes[i].IsGroup {
				if p >= 0 {
					parent[group[i]] = group[p]
				} else {
					parent[group[i]] = -1
				}
			} else {
				leaf[i] = len(lnodes)
				if vertical {
					lnodes = append(lnodes, Lnode(nodes[i].W, nodes[i].H, KindNode))
				} else {
					lnodes = append(lnodes, Lnode(nodes[i].H, nodes[i].W, KindNode))
				}
				if p >= 0 {
					of = append(of, group[p])
				} else {
					of = append(of, -1)
				}
			}
		}
		var ledges []*LEdge
		ledgeOf := fillInt(len(edges), -1)
		for ei, e := range edges {
			if e.Src == e.Dst || leaf[e.Src] < 0 || leaf[e.Dst] < 0 {
				continue
			}
			ledgeOf[ei] = len(ledges)
			if vertical {
				ledges = append(ledges, Ledge(leaf[e.Src], leaf[e.Dst], e.Minlen, e.LabelW, e.LabelH))
			} else {
				ledges = append(ledges, Ledge(leaf[e.Src], leaf[e.Dst], e.Minlen, e.LabelH, e.LabelW))
			}
		}
		flatOpt := opt
		flatOpt.Groups = &Groups{Of: of, Parent: parent}
		flatOpt.OrderOnly = true
		flatLayout := Layered(lnodes, ledges, flatOpt)
		flatCrossings = min(flatLayout.Crossings, flatLayout.Declared)
		sum := make([]float64, n)
		count := make([]int, n)
		// Last rank occupied by each group.
		last := fillInt(n, -1)
		for i := 0; i < n; i++ {
			if leaf[i] < 0 {
				continue
			}
			key[i] = lnodes[leaf[i]].X
			for p := nodes[i].Parent; p >= 0; p = nodes[p].Parent {
				sum[p] += key[i]
				count[p]++
				last[p] = max(last[p], lnodes[leaf[i]].Rank)
			}
		}
		for i := 0; i < n; i++ {
			if count[i] > 0 {
				key[i] = sum[i] / float64(count[i])
			}
		}
		// Order outgoing ports at the group's last rank; break ties using the next rank.
		farHint = func(lv *level, port *portEdge) float64 {
			le := ledgeOf[port.e]
			if le < 0 || lv.id < 0 || !port.out || port.side != opposite(startSide(lv.dir)) {
				return math.NaN()
			}
			points := ledges[le].Points
			inside := key[edges[port.e].Src]
			outside := 0.0
			for k := 0; k < len(points); k += 2 {
				if points[k] == float64(last[lv.id]) {
					inside = points[k+1]
				}
				if points[k] == float64(last[lv.id]+1) {
					outside = points[k+1]
				}
			}
			return inside + float64(outside*1e-6)
		}
	}

	// Reuse unchanged levels across layout attempts.
	type layoutOf struct {
		given  []float64
		lnodes []*LNode
		ledges []*LEdge
		size   LayeredResult
	}
	laid := map[int][]*layoutOf{}
	b2f := func(b bool) float64 {
		if b {
			return 1
		}
		return 0
	}
	// Object.is for numbers: NaN is NaN, and 0 is not -0.
	same := func(a, b float64) bool {
		if math.IsNaN(a) {
			return math.IsNaN(b)
		}
		return math.Float64bits(a) == math.Float64bits(b)
	}
	layOut := func(lv *level, lnodes []*LNode, ledges []*LEdge) ([]*LNode, []*LEdge, LayeredResult) {
		if !ordered {
			return lnodes, ledges, Layered(lnodes, ledges, opt)
		}
		given := make([]float64, 0, 7*len(lnodes)+9*len(ledges))
		for _, ln := range lnodes {
			given = append(given, ln.W, ln.H, float64(ln.Kind), ln.Pin, ln.Hint, ln.Key, ln.Span)
		}
		for _, le := range ledges {
			given = append(given, float64(le.Tail), float64(le.Head), float64(le.Minlen), le.LabelW, le.LabelH, le.TailDx, le.HeadDx, b2f(le.TailFixed), b2f(le.HeadFixed))
		}
		past := laid[lv.id]
		for _, p := range past {
			eq := len(p.given) == len(given)
			for i := 0; eq && i < len(given); i++ {
				eq = same(p.given[i], given[i])
			}
			if eq {
				return p.lnodes, p.ledges, p.size
			}
		}
		done := &layoutOf{given: given, lnodes: lnodes, ledges: ledges, size: Layered(lnodes, ledges, opt)}
		laid[lv.id] = append(past, done)
		return done.lnodes, done.ledges, done.size
	}

	total := CompoundResult{}
	place := func() {
		// Rank parents first to determine which group sides edges cross.
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

		// Lay out children first so their sizes are known.
		for q := len(order) - 1; q >= 0; q-- {
			lv := order[q]
			var group *CNode
			if lv.id >= 0 {
				group = nodes[lv.id]
			}
			vertical := lv.dir == "TB" || lv.dir == "BT"
			flip := lv.dir == "BT" || lv.dir == "RL"
			builtNodes, builtEdges, dummies := build(lv, true)
			lnodes, ledges, size := layOut(lv, builtNodes, builtEdges)
			contentW, contentH := size.Height, size.Width
			if vertical {
				contentW, contentH = size.Width, size.Height
			}
			padX, padTop, padBottom, minW := 0.0, 0.0, 0.0, 0.0
			if group != nil {
				padX, padTop, padBottom, minW = group.PadX, group.PadTop, group.PadBottom, group.MinW
			}
			boxW := math.Max(contentW+float64(2*padX), minW)
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
				var outward []*level
				for item := e.Src; nodes[item].Parent != lv.id; item = nodes[item].Parent {
					outward = append(outward, levelOf[nodes[item].Parent])
				}
				var inward []*level
				for item := e.Dst; nodes[item].Parent != lv.id; item = nodes[item].Parent {
					inward = append(inward, levelOf[nodes[item].Parent])
				}
				size := len(lv.routes[le.e])
				for _, l := range outward {
					size += len(l.portOf[le.e*2].route)
				}
				for _, l := range inward {
					size += len(l.portOf[le.e*2+1].route)
				}
				route := make([]float64, 0, size)
				for _, l := range outward {
					route = appendRoute(route, l.portOf[le.e*2].route, l.x, l.y)
				}
				route = appendRoute(route, lv.routes[le.e], lv.x, lv.y)
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
	}

	// The flat layout ignores group boxes, so independent ordering may yield fewer crossings.
	style := StyleOf(opt)
	keyed = flat
	place()
	crossings := -1
	if flat {
		crossings = RouteCrossings(nodes, edges, style)
		if float64(crossings) > float64(float64(flatCrossings)*FLAT_SLACK)+FLAT_SPARE {
			first := snapshot(nodes, edges, order, total)
			keyed = false
			place()
			own := RouteCrossings(nodes, edges, style)
			if own >= crossings {
				keyed = true
				total = restore(first, nodes, edges, order)
			} else {
				crossings = own
			}
		}
	}

	// Refine port order using routes outside each group. Skip this if independent ordering won;
	// it rarely helps enough to justify another layout.
	if keyed == flat && ordered {
		again := false
		for _, lv := range order {
			if lv.id < 0 {
				continue
			}
			box := nodes[lv.id]
			vertical := lv.dir == "TB" || lv.dir == "BT"
			end := opposite(startSide(lv.dir))
			var seen [][2]float64
			for _, port := range lv.ports {
				if port.side != end {
					continue
				}
				// Skip the straight run out of the port to find where the edge turns.
				route := edges[port.e].Route
				bx := lv.x + port.px
				by := lv.y + port.py
				at := -1
				for k := 0; k < len(route) && (at < 0 || port.out); k += 3 {
					if math.Abs(route[k]-bx) < 0.01 && math.Abs(route[k+1]-by) < 0.01 {
						at = k
					}
				}
				if at < 0 {
					continue
				}
				a := 1
				if vertical {
					a = 0
				}
				step := -3
				if port.out {
					step = 3
				}
				next := at + step
				for next >= 0 && next < len(route) && math.Abs(route[next+a]-route[at+a]) < IN_LINE {
					next += step
				}
				if next < 0 || next >= len(route) {
					continue
				}
				if vertical {
					port.hint = route[next+a] - (box.X - box.W/2)
					seen = append(seen, [2]float64{port.px, port.hint})
				} else {
					port.hint = route[next+a] - (box.Y - box.H/2)
					seen = append(seen, [2]float64{port.py, port.hint})
				}
			}
			slices.SortStableFunc(seen, func(p, q [2]float64) int { return sign(p[0] - q[0]) })
			for i := 1; i < len(seen); i++ {
				if seen[i-1][1] > seen[i][1] {
					again = true
				}
			}
		}
		if again {
			before := crossings
			if crossings < 0 {
				before = RouteCrossings(nodes, edges, style)
			}
			if before > 0 {
				first := snapshot(nodes, edges, order, total)
				place()
				if RouteCrossings(nodes, edges, style) >= before {
					total = restore(first, nodes, edges, order)
				}
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
