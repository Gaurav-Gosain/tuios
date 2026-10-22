package layout

import (
	"math"
	"testing"
)

// The cases pele 1.8.0 added to tests/layout/compound.test.ts: route crossings and the
// order of groups and their ports from a layout of the whole drawing.

var testOptions = LayeredOptions{NodeSep: 40, EdgeSep: 16, RankSep: 48, PortSep: 20}

func testEdge(src, dst int) *CEdge {
	return &CEdge{Src: src, Dst: dst, Minlen: 1, Route: []float64{}}
}

func onBorder(node *CNode, x, y float64) bool {
	dx := math.Abs(x - node.X)
	dy := math.Abs(y - node.Y)
	inside := dx <= node.W/2+0.5 && dy <= node.H/2+0.5
	edgeX := math.Abs(dx-node.W/2) <= 0.5
	edgeY := math.Abs(dy-node.H/2) <= 0.5
	return inside && (edgeX || edgeY)
}

func testGroup(seq int) *CNode {
	node := Cnode(0, 0, -1)
	node.IsGroup = true
	node.PadX = 20
	node.PadTop = 40
	node.PadBottom = 20
	node.Span = math.Inf(1)
	node.Seq = seq
	return node
}

// Issue #3: two chains in separate groups feed a store in a third group.
func fanIn() ([]*CNode, []*CEdge) {
	nodes := []*CNode{testGroup(100), testGroup(101), testGroup(102), Cnode(80, 60, 2)}
	var edges []*CEdge
	for _, g := range []int{0, 1} {
		for i := 0; i < 5; i++ {
			v := len(nodes)
			nodes = append(nodes, Cnode(40, 40, g))
			nodes[v].Seq = v
			if i > 0 {
				edges = append(edges, testEdge(v-1, v))
			}
			edges = append(edges, testEdge(v, 3))
		}
	}
	return nodes, edges
}

func TestRouteCrossingsSkipFans(t *testing.T) {
	// counts where routes cross, but not where edges that meet at one node fan out from it
	nodes := []*CNode{Cnode(40, 40, -1), Cnode(40, 40, -1), Cnode(40, 40, -1), Cnode(40, 40, -1)}
	for i, node := range nodes {
		node.X = float64(100 * (i % 2))
		node.Y = float64(200 * (i / 2))
	}
	crossed := []*CEdge{testEdge(0, 3), testEdge(1, 2)}
	crossed[0].Route = []float64{0, 20, 0, 100, 180, 0}
	crossed[1].Route = []float64{100, 20, 0, 0, 180, 0}
	if got := RouteCrossings(nodes, crossed, StyleCurved); got != 1 {
		t.Errorf("crossed: %d crossings, want 1", got)
	}
	fan := []*CEdge{testEdge(0, 2), testEdge(0, 3)}
	fan[0].Route = []float64{10, 20, 0, 0, 180, 0}
	fan[1].Route = []float64{-10, 20, 0, 100, 180, 0}
	if got := RouteCrossings(nodes, fan, StyleCurved); got != 0 {
		t.Errorf("fan: %d crossings, want 0", got)
	}
}

func TestRouteCrossingsSkipJoinedRuns(t *testing.T) {
	// does not count edges that join one straight run into the node they meet at as crossing
	nodes := []*CNode{Cnode(40, 40, -1), Cnode(40, 40, -1), Cnode(40, 40, -1)}
	nodes[1].X = 100
	nodes[2].X = 50
	nodes[2].Y = 300
	edges := []*CEdge{testEdge(0, 2), testEdge(1, 2)}
	edges[0].Route = []float64{0, 20, 0, 0, 200, 0, 50, 200, 0, 50, 280, 0}
	edges[1].Route = []float64{100, 20, 0, 100, 150, 0, 50, 150, 0, 50, 280, 0}
	if got := RouteCrossings(nodes, edges, StyleCurved); got != 0 {
		t.Errorf("joined runs: %d crossings, want 0", got)
	}
	fan, into := fanIn()
	opt := testOptions
	opt.Tracks = true
	CompoundLayout(fan, into, "TB", opt)
	if got := RouteCrossings(fan, into, StyleCurved); got != 0 {
		t.Errorf("fan in with tracks: %d crossings, want 0", got)
	}
}

func TestRouteCrossingsFollowTheDrawing(t *testing.T) {
	// counts crossings where the drawing has them, round its corners or at its steps
	nodes := []*CNode{Cnode(40, 40, -1), Cnode(40, 40, -1), Cnode(40, 40, -1), Cnode(40, 40, -1)}
	nodes[0].Y = -20
	nodes[1].X = 120
	nodes[1].Y = 100
	nodes[2].X = 5
	nodes[2].Y = 170
	nodes[3].X = 5
	nodes[3].Y = 40
	// Rounding the corner clears the other edge.
	corner := []*CEdge{testEdge(0, 1), testEdge(2, 3)}
	corner[0].Route = []float64{0, 0, 0, 0, 100, 0, 100, 100, 1}
	corner[1].Route = []float64{5, 150, 0, 5, 60, 0}
	for _, c := range []struct {
		style Style
		want  int
	}{{StyleCurved, 0}, {StyleStepped, 1}, {StyleLinear, 1}} {
		if got := RouteCrossings(nodes, corner, c.style); got != c.want {
			t.Errorf("corner in style %d: %d crossings, want %d", c.style, got, c.want)
		}
	}
	fan, into := fanIn()
	CompoundLayout(fan, into, "TB", testOptions)
	if got := RouteCrossings(fan, into, StyleCurved); got > 4 {
		t.Errorf("fan in: %d crossings, want at most 4", got)
	}
}

func TestFarPortsFollowTheirEdges(t *testing.T) {
	// orders the ports on the far side of a group by where their edges go, so that a fan into
	// one node does not cross between groups
	for _, dir := range []Dir{"TB", "LR"} {
		nodes := []*CNode{testGroup(100), testGroup(101), testGroup(102), Cnode(80, 40, 2)}
		var edges []*CEdge
		for _, g := range []int{0, 1} {
			for i := 0; i < 5; i++ {
				v := len(nodes)
				nodes = append(nodes, Cnode(100, 40, g))
				if i > 0 {
					edges = append(edges, testEdge(v-1, v))
				}
				edges = append(edges, testEdge(v, 3))
			}
		}
		for i, node := range nodes {
			if node.IsGroup {
				continue
			}
			node.Seq = i
			node.Span = 72
		}
		CompoundLayout(nodes, edges, dir, testOptions)
		across := 1
		if dir == "TB" {
			across = 0
		}
		at := func(e *CEdge, g int) float64 {
			for k := 0; k < len(e.Route); k += 3 {
				if onBorder(nodes[g], e.Route[k], e.Route[k+1]) {
					return e.Route[k+across]
				}
			}
			return math.NaN()
		}
		var fan []*CEdge
		for _, e := range edges {
			if e.Dst == 3 {
				fan = append(fan, e)
			}
		}
		leave := make([]float64, len(fan))
		enter := make([]float64, len(fan))
		for i, e := range fan {
			leave[i] = at(e, nodes[e.Src].Parent)
			enter[i] = at(e, 2)
		}
		for i := range fan {
			if math.IsNaN(leave[i]) || math.IsNaN(enter[i]) {
				t.Errorf("%s edge %d: no point on a border", dir, i)
			}
			for j := i + 1; j < len(fan); j++ {
				if sign(leave[i]-leave[j]) != sign(enter[i]-enter[j]) {
					t.Errorf("%s edges %d and %d: leave in one order and enter in the other", dir, i, j)
				}
			}
		}
	}
}

func TestRowOfGroupsIsOrderedAsOne(t *testing.T) {
	// orders the nodes of a row of groups as one drawing, so that the edges between them do not cross
	nodes := []*CNode{testGroup(100), testGroup(101), testGroup(102)}
	stage := func(g int) []int {
		var ids []int
		for range 5 {
			nodes = append(nodes, Cnode(80, 40, g))
			nodes[len(nodes)-1].Seq = len(nodes)
			ids = append(ids, len(nodes)-1)
		}
		return ids
	}
	a, b, c := stage(0), stage(1), stage(2)
	var edges []*CEdge
	for i, j := range []int{4, 2, 0, 3, 1} {
		edges = append(edges, testEdge(a[i], b[j]))
	}
	for i, j := range []int{1, 3, 0, 4, 2} {
		edges = append(edges, testEdge(b[i], c[j]))
	}
	CompoundLayout(nodes, edges, "LR", testOptions)
	if got := RouteCrossings(nodes, edges, StyleCurved); got != 0 {
		t.Errorf("%d crossings, want 0", got)
	}
}
