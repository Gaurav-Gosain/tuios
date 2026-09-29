package layout

import (
	"fmt"
	"slices"
	"testing"
)

var allSides = []Side{SideLeft, SideRight, SideUp, SideDown}

func (s Side) String() string {
	return [...]string{"left", "right", "up", "down"}[s]
}

// touching is every rectangle in rects that meets from along the side, with
// at least two cells in common across it (one, for a pane one cell across). slack is how far apart the facing
// edges may be: the gap between panes, plus one for a shared border.
func touching(from Rect, rects map[int]Rect, side Side, slack int) []int {
	var out []int
	for id, r := range rects {
		if r == from {
			continue
		}
		var near, far, overlap int
		switch side {
		case SideLeft:
			near, far = r.X+r.W, from.X
			overlap = min(r.Y+r.H, from.Y+from.H) - max(r.Y, from.Y)
		case SideRight:
			near, far = r.X, from.X+from.W
			overlap = min(r.Y+r.H, from.Y+from.H) - max(r.Y, from.Y)
		case SideUp:
			near, far = r.Y+r.H, from.Y
			overlap = min(r.X+r.W, from.X+from.W) - max(r.X, from.X)
		case SideDown:
			near, far = r.Y, from.Y+from.H
			overlap = min(r.X+r.W, from.X+from.W) - max(r.X, from.X)
		}
		across := min(from.H, r.H)
		if side == SideUp || side == SideDown {
			across = min(from.W, r.W)
		}
		if abs(near-far) <= slack && overlap >= min(2, across) {
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
