package layout

import (
	"math"
	"slices"
)

func plainTracks(seq []int, from, to, LO, HI []float64, track []int) (int, bool) {
	const exact = 1 << 40
	n := len(seq)
	ends := make([]float64, 0, 2*n)
	froms := make([]float64, 0, n)
	tos := make([]float64, 0, n)
	for _, k := range seq {
		if !(math.Abs(from[k]) < exact && math.Abs(to[k]) < exact) || !(LO[k] < HI[k]-IN_LINE) {
			return 0, false
		}
		ends = append(ends, LO[k], HI[k]-IN_LINE)
		froms = append(froms, from[k])
		tos = append(tos, to[k])
	}
	ends = distinct(ends)
	froms = distinct(froms)
	tos = distinct(tos)
	for _, vs := range [2][]float64{froms, tos} {
		for i := 1; i < len(vs); i++ {
			if math.Abs(vs[i]-vs[i-1]) < IN_LINE {
				return 0, false
			}
		}
	}
	rank := func(vs []float64, v float64) int32 {
		i, _ := slices.BinarySearch(vs, v)
		return int32(i)
	}

	cells := int32(len(ends))
	free := newFreeRuns(n)
	runs := make([][]coverRun, 0, 16)
	fromTracks := make([][]int32, len(froms))
	toTracks := make([][]int32, len(tos))
	var scratch []coverRun
	at := func(tr, x int32) (coverRun, bool) {
		r := runs[tr]
		j, _ := slices.BinarySearchFunc(r, x, func(c coverRun, x int32) int { return int(c.b - x - 1) })
		if j == len(r) || r[j].a > x {
			return coverRun{}, false
		}
		return r[j], true
	}
	fits := func(tr, x, f, t int32) bool {
		c, ok := at(tr, x)
		return !ok || c.f == f || c.t == t
	}
	empty := func(tr, x int32) bool {
		_, ok := at(tr, x)
		return !ok
	}
	rows := int32(0)
	for _, k := range seq {
		l, s := rank(ends, LO[k]), rank(ends, HI[k]-IN_LINE)
		f, t := rank(froms, from[k]), rank(tos, to[k])
		best := free.lowest(l, s, func(tr int32) bool { return tr < rows && empty(tr, l) && empty(tr, s-1) })
		if best < 0 {
			best = rows
		}
		a, b := fromTracks[f], toTracks[t]
		for len(a)+len(b) > 0 {
			var tr int32
			if len(b) == 0 || (len(a) > 0 && a[0] <= b[0]) {
				tr, a = a[0], a[1:]
			} else {
				tr, b = b[0], b[1:]
			}
			if tr >= best {
				break
			}
			if fits(tr, l, f, t) && fits(tr, s-1, f, t) {
				best = tr
				break
			}
		}
		if best == rows {
			rows++
			runs = append(runs, []coverRun{{l, s, f, t}})
			if 0 < l {
				free.add(0, l, best)
			}
			if s < cells {
				free.add(s, cells, best)
			}
		} else {
			runs[best], scratch = cover(runs[best], scratch, l, s, f, t, cells, best, free)
		}
		track[k] = int(best)
		fromTracks[f] = insertSorted(fromTracks[f], best)
		toTracks[t] = insertSorted(toTracks[t], best)
	}
	return int(rows), true
}

func distinct(vs []float64) []float64 {
	slices.Sort(vs)
	return slices.Compact(vs)
}

func insertSorted(list []int32, v int32) []int32 {
	if i, found := slices.BinarySearch(list, v); !found {
		return slices.Insert(list, i, v)
	}
	return list
}

type coverRun struct{ a, b, f, t int32 }

func cover(r, scratch []coverRun, l, s, f, t, cells, tr int32, free *freeRuns) ([]coverRun, []coverRun) {
	mix := func(old, v int32) int32 {
		if old == v {
			return v
		}
		return -1
	}
	i0, _ := slices.BinarySearchFunc(r, l, func(c coverRun, l int32) int { return int(c.b - l - 1) })
	i1 := i0
	for i1 < len(r) && r[i1].a < s {
		i1++
	}
	hole := func(j int, from, to int32) {
		start, end := int32(0), cells
		if j > 0 {
			start = r[j-1].b
		}
		if j < len(r) {
			end = r[j].a
		}
		free.remove(start, end, tr)
		if start < from {
			free.add(start, from, tr)
		}
		if to < end {
			free.add(to, end, tr)
		}
	}
	out := scratch[:0]
	lo, hi := i0, i1
	if lo > 0 {
		lo--
		out = append(out, r[lo])
	}
	cur := l
	for j := i0; j < i1; j++ {
		g := r[j]
		if g.a < l {
			out = append(out, coverRun{g.a, l, g.f, g.t})
		}
		if cur < g.a {
			hole(j, cur, g.a)
			out = append(out, coverRun{cur, g.a, f, t})
		}
		end := min(g.b, s)
		out = append(out, coverRun{max(g.a, l), end, mix(g.f, f), mix(g.t, t)})
		if g.b > s {
			out = append(out, coverRun{s, g.b, g.f, g.t})
		}
		cur = end
	}
	if cur < s {
		hole(i1, cur, s)
		out = append(out, coverRun{cur, s, f, t})
	}
	if hi < len(r) {
		out = append(out, r[hi])
		hi++
	}
	w := 0
	for _, c := range out {
		if w > 0 && out[w-1].b == c.a && out[w-1].f == c.f && out[w-1].t == c.t {
			out[w-1].b = c.b
			continue
		}
		out[w] = c
		w++
	}
	out = out[:w]
	return slices.Replace(r, lo, hi, out...), out
}

type freeRuns struct {
	size   int
	root   []int32
	nodes  []freeNode
	unused []int32
	seed   uint32
}

type freeNode struct {
	a, b, tr, most, left, right int32
	prio                        uint32
}

const trackBlock = 64

func newFreeRuns(tracks int) *freeRuns {
	size := 1
	for size*trackBlock < tracks {
		size *= 2
	}
	return &freeRuns{
		size:  size,
		root:  make([]int32, 2*size),
		nodes: make([]freeNode, 1, 1+tracks),
		seed:  0x9e3779b9,
	}
}

func (h *freeRuns) lowest(l, s int32, holds func(tr int32) bool) int32 {
	node := 1
	if h.reach(h.root[node], l) < s {
		return -1
	}
	for node < h.size {
		node *= 2
		if h.reach(h.root[node], l) < s {
			node++
		}
	}
	for tr := int32((node - h.size) * trackBlock); ; tr++ {
		if holds(tr) {
			return tr
		}
	}
}

func (h *freeRuns) reach(x, l int32) int32 {
	best := int32(-1)
	for x != 0 {
		n := &h.nodes[x]
		if n.a <= l {
			best = max(best, n.b)
			if n.left != 0 {
				best = max(best, h.nodes[n.left].most)
			}
			x = n.right
		} else {
			x = n.left
		}
	}
	return best
}

func (h *freeRuns) add(a, b, tr int32) {
	for node := int(tr)/trackBlock + h.size; node > 0; node /= 2 {
		h.root[node] = h.insert(h.root[node], h.alloc(a, b, tr))
	}
}

func (h *freeRuns) remove(a, b, tr int32) {
	for node := int(tr)/trackBlock + h.size; node > 0; node /= 2 {
		h.root[node] = h.delete(h.root[node], a, tr)
	}
}

func (h *freeRuns) alloc(a, b, tr int32) int32 {
	h.seed ^= h.seed << 13
	h.seed ^= h.seed >> 17
	h.seed ^= h.seed << 5
	n := freeNode{a: a, b: b, tr: tr, most: b, prio: h.seed}
	if k := len(h.unused); k > 0 {
		x := h.unused[k-1]
		h.unused = h.unused[:k-1]
		h.nodes[x] = n
		return x
	}
	h.nodes = append(h.nodes, n)
	return int32(len(h.nodes) - 1)
}

func (h *freeRuns) before(x int32, a, tr int32) bool {
	n := &h.nodes[x]
	return n.a < a || (n.a == a && n.tr < tr)
}

func (h *freeRuns) pull(x int32) {
	n := &h.nodes[x]
	n.most = n.b
	if n.left != 0 {
		n.most = max(n.most, h.nodes[n.left].most)
	}
	if n.right != 0 {
		n.most = max(n.most, h.nodes[n.right].most)
	}
}

func (h *freeRuns) split(x, a, tr int32) (int32, int32) {
	if x == 0 {
		return 0, 0
	}
	if h.before(x, a, tr) {
		l, r := h.split(h.nodes[x].right, a, tr)
		h.nodes[x].right = l
		h.pull(x)
		return x, r
	}
	l, r := h.split(h.nodes[x].left, a, tr)
	h.nodes[x].left = r
	h.pull(x)
	return l, x
}

func (h *freeRuns) join(l, r int32) int32 {
	if l == 0 {
		return r
	}
	if r == 0 {
		return l
	}
	if h.nodes[l].prio > h.nodes[r].prio {
		h.nodes[l].right = h.join(h.nodes[l].right, r)
		h.pull(l)
		return l
	}
	h.nodes[r].left = h.join(l, h.nodes[r].left)
	h.pull(r)
	return r
}

func (h *freeRuns) insert(x, n int32) int32 {
	if x == 0 {
		return n
	}
	if h.nodes[n].prio > h.nodes[x].prio {
		l, r := h.split(x, h.nodes[n].a, h.nodes[n].tr)
		h.nodes[n].left, h.nodes[n].right = l, r
		h.pull(n)
		return n
	}
	if h.before(n, h.nodes[x].a, h.nodes[x].tr) {
		h.nodes[x].left = h.insert(h.nodes[x].left, n)
	} else {
		h.nodes[x].right = h.insert(h.nodes[x].right, n)
	}
	h.pull(x)
	return x
}

func (h *freeRuns) delete(x, a, tr int32) int32 {
	if x == 0 {
		return 0
	}
	if n := h.nodes[x]; n.a == a && n.tr == tr {
		h.unused = append(h.unused, x)
		return h.join(n.left, n.right)
	}
	if h.before(x, a, tr) {
		h.nodes[x].right = h.delete(h.nodes[x].right, a, tr)
	} else {
		h.nodes[x].left = h.delete(h.nodes[x].left, a, tr)
	}
	h.pull(x)
	return x
}
