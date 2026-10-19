package layout

import (
	"math"
	"testing"
)

// FuzzCompoundLayout lays out graphs built from fuzz bytes: up to 48 nodes, some of
// them groups with parents (cycles of parents included), and up to 160 edges with
// labels, self-loops and long spans. The layout must not panic and must give
// finite coordinates.
func FuzzCompoundLayout(f *testing.F) {
	f.Add([]byte{4, 3, 0, 1, 1, 2, 2, 3, 3, 0})
	f.Add([]byte{12, 9, 200, 1, 7, 0, 2, 5, 9, 1, 3, 3, 4, 8, 6, 2, 11, 0, 1, 5, 5, 1, 2, 3, 4, 5, 6, 7, 8, 9})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 2 {
			return
		}
		next := func() int {
			if len(data) == 0 {
				return 0
			}
			b := int(data[0])
			data = data[1:]
			return b
		}
		n := next()%48 + 1
		m := next() % 160
		nodes := make([]*CNode, n)
		for i := range nodes {
			b := next()
			c := Cnode(float64(10+b%120), float64(10+(b/3)%60), -1)
			if b%7 == 0 {
				c.IsGroup = true
				c.PadX, c.PadTop, c.PadBottom = 20, 30, 20
				c.Dir = []Dir{"", "TB", "LR", "BT", "RL"}[b%5]
			}
			if b%5 == 1 {
				c.Parent = next() % n
			}
			c.Seq = i
			c.Span = float64(b % 90)
			nodes[i] = c
		}
		edges := make([]*CEdge, m)
		for i := range edges {
			e := &CEdge{Src: next() % n, Dst: next() % n, Minlen: 1 + next()%3, Route: []float64{}}
			if b := next(); b%4 == 0 {
				e.LabelW, e.LabelH = float64(20+b%80), 24
			}
			edges[i] = e
		}
		dir := []Dir{"TB", "LR", "BT", "RL"}[next()%4]
		opt := LayeredOptions{NodeSep: 40, EdgeSep: 16, RankSep: 48, PortSep: 20, HeadRoom: 7, Tracks: next()%5 == 0}
		res := CompoundLayout(nodes, edges, dir, opt)
		bad := func(v float64) bool { return math.IsNaN(v) || math.IsInf(v, 0) }
		if bad(res.Width) || bad(res.Height) {
			t.Fatalf("size %v x %v", res.Width, res.Height)
		}
		for i, c := range nodes {
			if bad(c.X) || bad(c.Y) {
				t.Fatalf("node %d at %v,%v", i, c.X, c.Y)
			}
		}
		for i, e := range edges {
			for _, v := range e.Route {
				if bad(v) {
					t.Fatalf("edge %d route %v", i, e.Route)
				}
			}
		}
	})
}
