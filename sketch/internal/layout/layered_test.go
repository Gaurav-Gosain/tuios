package layout

import "testing"

// The case pele 1.8.0 added in tests/layout/layered.test.ts.

func TestGroupedOrderCountsItsCrossings(t *testing.T) {
	// orders a flat graph with groups side by side, and counts the crossings of that order
	nodes := make([]*LNode, 6)
	for i := range nodes {
		nodes[i] = Lnode(40, 40, KindNode)
	}
	of := []int{0, 1, 0, -1, 0, -1}
	edges := []*LEdge{Ledge(0, 4, 1, 0, 0), Ledge(1, 4, 1, 0, 0), Ledge(2, 5, 1, 0, 0), Ledge(3, 4, 1, 0, 0), Ledge(3, 5, 1, 0, 0)}
	res := Layered(nodes, edges, LayeredOptions{
		NodeSep:   40,
		EdgeSep:   16,
		RankSep:   48,
		PortSep:   20,
		Groups:    &Groups{Of: of, Parent: []int{-1, -1}},
		OrderOnly: true,
	})
	// Points are (rank, cross-flow position) pairs.
	var pieces [][3]float64
	for _, e := range edges {
		for k := 2; k < len(e.Points); k += 2 {
			pieces = append(pieces, [3]float64{e.Points[k-2], e.Points[k-1], e.Points[k+1]})
		}
	}
	crossed := 0
	for i := 0; i < len(pieces); i++ {
		for j := i + 1; j < len(pieces); j++ {
			r, a0, a1 := pieces[i][0], pieces[i][1], pieces[i][2]
			s, b0, b1 := pieces[j][0], pieces[j][1], pieces[j][2]
			if r == s && float64(a0-b0)*float64(a1-b1) < 0 {
				crossed++
			}
		}
	}
	if res.Crossings != crossed {
		t.Errorf("crossings %d, want %d", res.Crossings, crossed)
	}
	for i := range nodes {
		for j := range nodes {
			if of[i] < 0 || of[j] != of[i] || nodes[i].Rank != nodes[j].Rank || nodes[i].X >= nodes[j].X {
				continue
			}
			for k := range nodes {
				between := nodes[k].Rank == nodes[i].Rank && nodes[k].X > nodes[i].X && nodes[k].X < nodes[j].X
				if between && of[k] != of[i] {
					t.Errorf("node %d between %d and %d", k, i, j)
				}
			}
		}
	}
}
