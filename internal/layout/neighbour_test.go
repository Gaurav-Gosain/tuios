package layout

import (
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"testing"
)

var allSides = []Side{SideLeft, SideRight, SideUp, SideDown}

func (s Side) String() string {
	return [...]string{"left", "right", "up", "down"}[s]
}

// touching is every rectangle in rects that meets from along the side, with
// at least one cell in common across it. slack is how far past from's edge the
// facing edge may start: the gap between panes, plus one. A shared border puts
// it one cell before from's edge, which also counts.
func touching(from Rect, rects map[int]Rect, side Side, slack int) []int {
	var out []int
	for id, r := range rects {
		if r == from {
			continue
		}
		// Turned so that side is right: dist is how far r starts past from's
		// right edge, negative when r starts before it.
		f, c := towardRight(from, side), towardRight(r, side)
		dist := c.X - (f.X + f.W)
		overlap := min(c.Y+c.H, f.Y+f.H) - max(c.Y, f.Y)
		if dist >= -1 && dist <= slack && overlap >= 1 {
			out = append(out, id)
		}
	}
	return out
}

// checkEveryStep steps from every pane in every direction and checks that
// focus lands on a pane touching it on that side, that it stays put only at
// the edge of the layout, and that every pane can be reached from the first.
func checkEveryStep(t *testing.T, rects map[int]Rect, slack int) {
	t.Helper()
	ids := make([]int, 0, len(rects))
	for id := range rects {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	step := func(from int, side Side) int {
		var cands []Rect
		var index []int
		for _, id := range ids {
			if id != from {
				cands = append(cands, rects[id])
				index = append(index, id)
			}
		}
		if n := Neighbour(rects[from], cands, side, false); n >= 0 {
			return index[n]
		}
		return -1
	}
	for _, id := range ids {
		for _, side := range allSides {
			want := touching(rects[id], rects, side, slack)
			got := step(id, side)
			switch {
			case len(want) == 0 && got != -1:
				t.Errorf("pane %d %+v: %s went to %d %+v, but nothing touches it on that side",
					id, rects[id], side, got, rects[got])
			case len(want) > 0 && !slices.Contains(want, got):
				t.Errorf("pane %d %+v: %s went to %d, want one of %v", id, rects[id], side, got, want)
			}
		}
	}
	seen := map[int]bool{ids[0]: true}
	queue := []int{ids[0]}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, side := range allSides {
			if n := step(cur, side); n >= 0 && !seen[n] {
				seen[n] = true
				queue = append(queue, n)
			}
		}
	}
	if len(seen) != len(ids) {
		t.Errorf("directional steps reach %d of %d panes: %v", len(seen), len(ids), rects)
	}
}

// Issue #231 on the real tiler: every pane of a BSP spiral, from two panes to
// nine, on a wide and a tall screen, with and without a gap between panes.
func TestNeighbourOnBSPSpirals(t *testing.T) {
	for _, bounds := range []Rect{{0, 0, 160, 46}, {0, 1, 80, 60}} {
		for _, gap := range []int{0, 1} {
			for n := 2; n <= 9; n++ {
				t.Run(fmt.Sprintf("%dx%d gap %d %d panes", bounds.W, bounds.H, gap, n), func(t *testing.T) {
					tree := NewBSPTree()
					last := 0
					for i := 1; i <= n; i++ {
						tree.InsertWindow(i, last, SplitNone, 0.5, bounds, gap)
						last = i
					}
					checkEveryStep(t, tree.ApplyLayout(bounds, gap), gap+1)
				})
			}
		}
	}
}

// Hand-built trees, for the nestings the spiral never makes: a vertical split
// inside a horizontal one inside a vertical one, and panes that do not line up
// with anything on the far side of a split.
func TestNeighbourOnNestedBSPTrees(t *testing.T) {
	leaf := NewLeafNode
	v := func(ratio float64, l, r *TileNode) *TileNode { return NewInternalNode(SplitVertical, ratio, l, r) }
	h := func(ratio float64, l, r *TileNode) *TileNode { return NewInternalNode(SplitHorizontal, ratio, l, r) }

	trees := map[string]*TileNode{
		// Left column of three, right column of two: the rows do not line up.
		"3 beside 2": v(0.5, h(0.33, leaf(1), h(0.5, leaf(2), leaf(3))), h(0.5, leaf(4), leaf(5))),
		// A 2x2 grid whose top right is split again both ways.
		"grid nested twice": h(0.5,
			v(0.5, leaf(1), h(0.5, leaf(2), v(0.5, leaf(3), leaf(4)))),
			v(0.5, leaf(5), leaf(6))),
		// A wide top row over three columns, the middle one stacked.
		"banner over columns": h(0.3, leaf(1), v(0.33, leaf(2), v(0.5, h(0.5, leaf(3), leaf(4)), leaf(5)))),
		// Uneven ratios, so the edges of neighbours are offset by odd amounts.
		"uneven": v(0.7, h(0.2, leaf(1), v(0.4, leaf(2), leaf(3))), h(0.8, v(0.3, leaf(4), leaf(5)), leaf(6))),
	}
	for name, root := range trees {
		for _, gap := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s gap %d", name, gap), func(t *testing.T) {
				tree := &BSPTree{Root: root}
				bounds := Rect{0, 0, 157, 47}
				checkEveryStep(t, tree.ApplyLayout(bounds, gap), gap+1)
			})
		}
	}
}

// Master-stack, grid included.
func TestNeighbourOnMasterStack(t *testing.T) {
	for n := 2; n <= 9; n++ {
		t.Run(fmt.Sprintf("%d panes", n), func(t *testing.T) {
			rects := map[int]Rect{}
			for i, l := range CalculateMasterStackLayout(n, 160, 46, 0, 0, 0, 1) {
				rects[i] = Rect{l.X, l.Y, l.Width, l.Height}
			}
			checkEveryStep(t, rects, 2)
		})
	}
}

// A pane with borders shared with its neighbours overlaps each of them by one
// cell. Down from the short pane must reach the pane under it, not the tall
// pane on its left, whose one shared column is the only thing they have in
// common.
//
//	+-----+----+
//	|     | p  |
//	|  l  +----+
//	|     | b  |
//	+-----+----+
func TestNeighbourIgnoresASharedCorner(t *testing.T) {
	p := Rect{X: 60, Y: 0, W: 30, H: 4}
	cands := []Rect{
		{X: 0, Y: 0, W: 61, H: 40},  // l
		{X: 60, Y: 3, W: 30, H: 37}, // b
	}
	if got := Neighbour(p, cands, SideDown, false); got != 1 {
		t.Fatalf("down from the short pane went to %d, want 1 (the pane under it)", got)
	}
	if got := Neighbour(p, cands, SideLeft, false); got != 0 {
		t.Fatalf("left from the short pane went to %d, want 0", got)
	}
}

// Floating windows need not line up. A window that lies that way without
// facing the focused one is reached only with the fallback, which tiling
// leaves off.
func TestNeighbourFallbackForFloatingWindows(t *testing.T) {
	from := Rect{X: 0, Y: 0, W: 40, H: 10}
	diagonal := []Rect{{X: 50, Y: 20, W: 40, H: 10}}
	if got := Neighbour(from, diagonal, SideRight, false); got != -1 {
		t.Fatalf("without the fallback right went to %d, want nothing", got)
	}
	if got := Neighbour(from, diagonal, SideRight, true); got != 0 {
		t.Fatalf("with the fallback right went to %d, want 0", got)
	}
	if got := Neighbour(from, diagonal, SideDown, true); got != 0 {
		t.Fatalf("with the fallback down went to %d, want 0", got)
	}
	if got := Neighbour(from, diagonal, SideLeft, true); got != -1 {
		t.Fatalf("left went to %d, but nothing lies that way", got)
	}

	// A facing window beats a nearer diagonal one.
	cands := []Rect{{X: 42, Y: 11, W: 10, H: 5}, {X: 80, Y: 2, W: 20, H: 6}}
	if got := Neighbour(from, cands, SideRight, true); got != 1 {
		t.Fatalf("right went to %d, want the facing window 1", got)
	}

	// A window that half covers the focused one still lies that way.
	over := []Rect{{X: 20, Y: 3, W: 40, H: 10}}
	if got := Neighbour(from, over, SideRight, true); got != 0 {
		t.Fatalf("right went to %d, want the overlapping window", got)
	}
	if got := Neighbour(from, over, SideDown, true); got != 0 {
		t.Fatalf("down went to %d, want the overlapping window", got)
	}
}

// Among panes at the same distance, the one centred nearest wins, then the
// earlier one.
func TestNeighbourPrefersTheAlignedPane(t *testing.T) {
	from := Rect{X: 0, Y: 20, W: 50, H: 10}
	cands := []Rect{
		{X: 50, Y: 0, W: 50, H: 22},  // shares 2 rows at the top
		{X: 50, Y: 22, W: 50, H: 6},  // centred on from
		{X: 50, Y: 28, W: 50, H: 20}, // shares 2 rows at the bottom
	}
	if got := Neighbour(from, cands, SideRight, false); got != 1 {
		t.Fatalf("right went to %d, want the centred pane 1", got)
	}
	tied := []Rect{{X: 50, Y: 0, W: 50, H: 25}, {X: 50, Y: 25, W: 50, H: 25}}
	if got := Neighbour(Rect{X: 0, Y: 0, W: 50, H: 50}, tied, SideRight, false); got != 0 {
		t.Fatalf("right went to %d, want the earlier of two tied panes", got)
	}
}

// A pane can meet its only neighbour on a side along a single row. That one
// row is enough to step there. Both layouts come from the BSP tiler.
func TestNeighbourAcrossASingleSharedRow(t *testing.T) {
	// 200x60, gap 1: both panes on the right share one row with from.
	from := Rect{X: 0, Y: 8, W: 92, H: 3}
	right := []Rect{{X: 93, Y: 0, W: 107, H: 9}, {X: 93, Y: 10, W: 21, H: 20}}
	if got := Neighbour(from, right, SideRight, false); got != 0 {
		t.Fatalf("right went to %d, want 0 (centred nearest)", got)
	}

	// 81x23, gap 2: the pane on the left shares one row with from.
	from = Rect{X: 27, Y: 12, W: 14, H: 3}
	left := []Rect{{X: 0, Y: 14, W: 25, H: 9}}
	if got := Neighbour(from, left, SideLeft, false); got != 0 {
		t.Fatalf("left went to %d, want 0", got)
	}
}

// At the same distance, a pane sharing two or more cells beats one sharing a
// single cell, even when the thin one is centred closer.
func TestNeighbourPrefersTheWiderOverlap(t *testing.T) {
	from := Rect{X: 0, Y: 10, W: 50, H: 10}
	cands := []Rect{
		{X: 50, Y: 0, W: 50, H: 11},  // one row, centred closer
		{X: 50, Y: 18, W: 50, H: 30}, // two rows
	}
	if got := Neighbour(from, cands, SideRight, false); got != 1 {
		t.Fatalf("right went to %d, want 1 (two shared rows)", got)
	}
}

// Random BSP trees from a fixed seed, with every split direction and ratio and
// gaps up to 3. Layouts with a pane smaller than 4x3 are skipped: the tiler
// leaves holes there, so no step is well defined.
func TestNeighbourOnRandomBSPTrees(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	var build func(n int, next *int) *TileNode
	build = func(n int, next *int) *TileNode {
		if n == 1 {
			*next++
			return NewLeafNode(*next)
		}
		k := 1 + r.Intn(n-1)
		split := SplitVertical
		if r.Intn(2) == 0 {
			split = SplitHorizontal
		}
		ratio := 0.1 + r.Float64()*0.8
		return NewInternalNode(split, ratio, build(k, next), build(n-k, next))
	}
	sizes := []Rect{{0, 0, 81, 23}, {0, 0, 160, 46}, {0, 0, 200, 60}, {0, 0, 37, 11}}
	checked := 0
	for range 20000 {
		n := 2 + r.Intn(12)
		bounds := sizes[r.Intn(len(sizes))]
		gap := r.Intn(4)
		next := 0
		tree := &BSPTree{Root: build(n, &next)}
		rects := tree.ApplyLayout(bounds, gap)
		if len(rects) != n || slices.ContainsFunc(slices.Collect(maps.Values(rects)), func(p Rect) bool {
			return p.W < 4 || p.H < 3
		}) {
			continue
		}
		checked++
		checkEveryStep(t, rects, gap+1)
		if t.Failed() {
			t.Fatalf("%dx%d gap %d: %v", bounds.W, bounds.H, gap, rects)
		}
	}
	if checked < 1000 {
		t.Fatalf("only %d layouts checked, want at least 1000", checked)
	}
}
