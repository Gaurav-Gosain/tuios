// Ported from pele src/layout/components.ts at 8aff183be57021b533f319a29341a529be934b08.

package layout

import "math"

type part struct {
	nodes  []*CNode
	edges  []*CEdge
	left   float64
	top    float64
	right  float64
	bottom float64
}

func (p *part) include(x, y, w, h float64) {
	p.left = math.Min(p.left, x-w/2)
	p.top = math.Min(p.top, y-h/2)
	p.right = math.Max(p.right, x+w/2)
	p.bottom = math.Max(p.bottom, y+h/2)
}

// WrapComponents moves disconnected parts into rows without changing the layout inside a part.
// It gives false when the parts already fit, or when rows would be no narrower.
func WrapComponents(nodes []*CNode, edges []*CEdge, width, gap float64) (CompoundResult, bool) {
	parent := make([]int32, len(nodes))
	for i := range parent {
		parent[i] = int32(i)
	}
	root := func(i int) int {
		for int(parent[i]) != i {
			parent[i] = parent[parent[i]]
			i = int(parent[i])
		}
		return i
	}
	join := func(a, b int) { parent[root(a)] = int32(root(b)) }
	for i, c := range nodes {
		if c.Parent >= 0 {
			join(i, c.Parent)
		}
	}
	for _, e := range edges {
		join(e.Src, e.Dst)
	}

	// The parts in the order their first node appears, as a JavaScript Map keeps them.
	var parts []*part
	index := map[int]int{}
	for i, c := range nodes {
		id := root(i)
		k, ok := index[id]
		if !ok {
			k = len(parts)
			index[id] = k
			parts = append(parts, &part{left: math.Inf(1), top: math.Inf(1), right: math.Inf(-1), bottom: math.Inf(-1)})
		}
		p := parts[k]
		p.nodes = append(p.nodes, c)
		p.include(c.X, c.Y, c.W, c.H)
	}
	if len(parts) < 2 {
		return CompoundResult{}, false
	}
	for _, e := range edges {
		p := parts[index[root(e.Src)]]
		p.edges = append(p.edges, e)
		for i := 0; i+1 < len(e.Route); i += 3 {
			p.include(e.Route[i], e.Route[i+1], 0, 0)
		}
		if e.LabelW > 0 {
			p.include(e.LabelX, e.LabelY, e.LabelW, e.LabelH)
		}
	}

	x, y, rowHeight, right, from, to := 0.0, 0.0, 0.0, 0.0, math.Inf(1), math.Inf(-1)
	shifts := make([]float64, 0, 2*len(parts))
	for _, p := range parts {
		w, h := p.right-p.left, p.bottom-p.top
		if x > 0 && x+w > width {
			y += rowHeight + gap
			x = 0
			rowHeight = 0
		}
		shifts = append(shifts, x-p.left, y-p.top)
		from = math.Min(from, p.left)
		to = math.Max(to, p.right)
		right = math.Max(right, x+w)
		rowHeight = math.Max(rowHeight, h)
		x += w + gap
	}
	if right >= to-from {
		return CompoundResult{}, false
	}
	for k, p := range parts {
		ShiftLayout(p.nodes, p.edges, shifts[2*k], shifts[2*k+1])
	}
	return CompoundResult{Width: right, Height: y + rowHeight}, true
}
