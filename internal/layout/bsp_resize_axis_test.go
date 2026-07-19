package layout

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// edge identifies which side of a pane a resize drags.
type edge int

const (
	edgeRight edge = iota
	edgeLeft
	edgeBottom
	edgeTop
)

func (e edge) horizontal() bool { return e == edgeRight || e == edgeLeft }

func (e edge) String() string {
	switch e {
	case edgeRight:
		return "right"
	case edgeLeft:
		return "left"
	case edgeBottom:
		return "bottom"
	default:
		return "top"
	}
}

// leaf builds a leaf and registers it with the tree.
func (t *BSPTree) leaf(id int) *TileNode {
	n := NewLeafNode(id)
	t.WindowToNode[id] = n
	return n
}

func treeOf(build func(t *BSPTree) *TileNode) *BSPTree {
	t := NewBSPTree()
	t.Root = build(t)
	return t
}

// twoPanes: 1 | 2
func twoPanes() *BSPTree {
	return treeOf(func(t *BSPTree) *TileNode {
		return NewInternalNode(SplitVertical, 0.5, t.leaf(1), t.leaf(2))
	})
}

// tallLeft is the maintainer's layout: one full-height column on the left, the
// right column split into top and bottom.
func tallLeft() *BSPTree {
	return treeOf(func(t *BSPTree) *TileNode {
		right := NewInternalNode(SplitHorizontal, 0.5, t.leaf(2), t.leaf(3))
		return NewInternalNode(SplitVertical, 0.5, t.leaf(1), right)
	})
}

// tallRight is the mirror image: the stacked column is on the left.
func tallRight() *BSPTree {
	return treeOf(func(t *BSPTree) *TileNode {
		left := NewInternalNode(SplitHorizontal, 0.5, t.leaf(1), t.leaf(2))
		return NewInternalNode(SplitVertical, 0.5, left, t.leaf(3))
	})
}

// grid is a 2x2 arrangement built from two horizontal splits under a vertical one.
func grid() *BSPTree {
	return treeOf(func(t *BSPTree) *TileNode {
		left := NewInternalNode(SplitHorizontal, 0.5, t.leaf(1), t.leaf(2))
		right := NewInternalNode(SplitHorizontal, 0.5, t.leaf(3), t.leaf(4))
		return NewInternalNode(SplitVertical, 0.5, left, right)
	})
}

// deepNest puts a pane three splits down so a resize has to survive several
// levels of bounds narrowing.
func deepNest() *BSPTree {
	return treeOf(func(t *BSPTree) *TileNode {
		inner := NewInternalNode(SplitVertical, 0.5, t.leaf(3), t.leaf(4))
		mid := NewInternalNode(SplitHorizontal, 0.5, t.leaf(2), inner)
		right := NewInternalNode(SplitHorizontal, 0.6, mid, t.leaf(5))
		return NewInternalNode(SplitVertical, 0.4, t.leaf(1), right)
	})
}

func sharedGap() int {
	if config.SharedBorders {
		return 1
	}
	return 0
}

// dragEdge moves one edge of the target pane and drags every pane that shares
// that divider along with it, the way adjustTilingNeighborsGeneric does. It
// returns false when the move would push any pane below the minimum size, so
// callers can skip a case rather than assert against clamped geometry.
func dragEdge(geo map[int]Rect, target int, e edge, delta int) (map[int]Rect, bool) {
	gap := sharedGap()
	r, ok := geo[target]
	if !ok {
		return nil, false
	}

	out := make(map[int]Rect, len(geo))
	for id, g := range geo {
		out[id] = g
	}

	if e.horizontal() {
		old := r.X
		if e == edgeRight {
			old = r.X + r.W
		}
		moved := old + delta
		for id, g := range out {
			switch {
			case g.X+g.W == old:
				g.W = moved - g.X
			case g.X == old+gap:
				g.W = g.X + g.W - (moved + gap)
				g.X = moved + gap
			default:
				continue
			}
			if g.W < config.DefaultWindowWidth {
				return nil, false
			}
			out[id] = g
		}
		return out, true
	}

	old := r.Y
	if e == edgeBottom {
		old = r.Y + r.H
	}
	moved := old + delta
	for id, g := range out {
		switch {
		case g.Y+g.H == old:
			g.H = moved - g.Y
		case g.Y == old+gap:
			g.H = g.Y + g.H - (moved + gap)
			g.Y = moved + gap
		default:
			continue
		}
		if g.H < config.DefaultWindowHeight {
			return nil, false
		}
		out[id] = g
	}
	return out, true
}

// assertAxisIsolated checks the invariant: a horizontal resize may only change
// X and W, a vertical resize may only change Y and H.
func assertAxisIsolated(t *testing.T, label string, e edge, before, after map[int]Rect) {
	t.Helper()
	for id, b := range before {
		a, ok := after[id]
		if !ok {
			t.Fatalf("%s: pane %d vanished from layout", label, id)
		}
		if e.horizontal() {
			if a.Y != b.Y || a.H != b.H {
				t.Errorf("%s: horizontal resize moved pane %d vertically: Y %d->%d, H %d->%d",
					label, id, b.Y, a.Y, b.H, a.H)
			}
		} else if a.X != b.X || a.W != b.W {
			t.Errorf("%s: vertical resize moved pane %d horizontally: X %d->%d, W %d->%d",
				label, id, b.X, a.X, b.W, a.W)
		}
	}
}

// withSharedBorders runs fn with config.SharedBorders set, restoring it after.
func withSharedBorders(t *testing.T, shared bool, fn func()) {
	t.Helper()
	prev := config.SharedBorders
	config.SharedBorders = shared
	defer func() { config.SharedBorders = prev }()
	fn()
}

var resizeLayouts = []struct {
	name  string
	build func() *BSPTree
	panes []int
}{
	{"two_panes", twoPanes, []int{1, 2}},
	{"tall_left", tallLeft, []int{1, 2, 3}},
	{"tall_right", tallRight, []int{1, 2, 3}},
	{"grid", grid, []int{1, 2, 3, 4}},
	{"deep_nest", deepNest, []int{1, 2, 3, 4, 5}},
}

// TestResizeKeepsOffAxisGeometry is the core property: dragging any edge of any
// pane, then syncing the geometry back into the tree and re-applying the
// layout, must leave the perpendicular axis of every pane untouched.
func TestResizeKeepsOffAxisGeometry(t *testing.T) {
	bounds := Rect{X: 0, Y: 1, W: 160, H: 48}
	edges := []edge{edgeRight, edgeLeft, edgeBottom, edgeTop}

	for _, shared := range []bool{false, true} {
		withSharedBorders(t, shared, func() {
			for _, lay := range resizeLayouts {
				for _, pane := range lay.panes {
					for _, e := range edges {
						for _, delta := range []int{4, -4} {
							tree := lay.build()
							before := tree.ApplyLayout(bounds)
							dragged, ok := dragEdge(before, pane, e, delta)
							if !ok {
								continue
							}
							tree.SyncRatiosFromGeometry(dragged, bounds)
							after := tree.ApplyLayout(bounds)

							label := lay.name + "/pane" + string(rune('0'+pane)) + "/" + e.String()
							if shared {
								label = "shared:" + label
							}
							assertAxisIsolated(t, label, e, before, after)
						}
					}
				}
			}
		})
	}
}

// TestRepeatedResizeDoesNotDrift checks that many small drags land in the same
// place as one large drag and, more importantly, that the perpendicular axis
// never accumulates drift across a long sequence.
func TestRepeatedResizeDoesNotDrift(t *testing.T) {
	bounds := Rect{X: 0, Y: 1, W: 160, H: 48}

	for _, shared := range []bool{false, true} {
		withSharedBorders(t, shared, func() {
			for _, lay := range resizeLayouts {
				tree := lay.build()
				start := tree.ApplyLayout(bounds)
				geo := start

				const steps = 8
				for i := 0; i < steps; i++ {
					dragged, ok := dragEdge(geo, 1, edgeRight, 2)
					if !ok {
						break
					}
					tree.SyncRatiosFromGeometry(dragged, bounds)
					geo = tree.ApplyLayout(bounds)
				}

				label := lay.name
				if shared {
					label = "shared:" + label
				}
				assertAxisIsolated(t, label+"/repeated", edgeRight, start, geo)
			}
		})
	}
}

// TestSyncRoundTripIsStable checks that syncing an unmodified layout back into
// the tree and re-applying it is a no-op. Any movement here is pure rounding
// drift and would show up as panes creeping on every mouse motion event.
func TestSyncRoundTripIsStable(t *testing.T) {
	for _, shared := range []bool{false, true} {
		withSharedBorders(t, shared, func() {
			for _, lay := range resizeLayouts {
				for _, w := range []int{80, 97, 120, 160, 201} {
					for _, h := range []int{24, 31, 48, 60} {
						bounds := Rect{X: 0, Y: 1, W: w, H: h}
						tree := lay.build()
						before := tree.ApplyLayout(bounds)
						tree.SyncRatiosFromGeometry(before, bounds)
						after := tree.ApplyLayout(bounds)
						for id, b := range before {
							if after[id] != b {
								t.Errorf("shared=%v %s %dx%d: pane %d drifted on sync round trip: %+v -> %+v",
									shared, lay.name, w, h, id, b, after[id])
							}
						}
					}
				}
			}
		})
	}
}

// TestHorizontalResizeKeepsRightColumnHeights is the maintainer's exact report,
// pinned as a regression: a tall left pane plus a stacked right column, drag the
// left pane wider, the two right panes must keep their heights.
func TestHorizontalResizeKeepsRightColumnHeights(t *testing.T) {
	withSharedBorders(t, true, func() {
		bounds := Rect{X: 0, Y: 1, W: 80, H: 30}
		tree := tallLeft()
		before := tree.ApplyLayout(bounds)

		geo := before
		for i := 0; i < 4; i++ {
			dragged, ok := dragEdge(geo, 1, edgeRight, 4)
			if !ok {
				t.Fatalf("drag %d hit the minimum size", i)
			}
			tree.SyncRatiosFromGeometry(dragged, bounds)
			geo = tree.ApplyLayout(bounds)
		}

		if geo[2].H != before[2].H {
			t.Errorf("top-right height changed: %d -> %d", before[2].H, geo[2].H)
		}
		if geo[3].H != before[3].H || geo[3].Y != before[3].Y {
			t.Errorf("bottom-right moved: Y %d->%d H %d->%d",
				before[3].Y, geo[3].Y, before[3].H, geo[3].H)
		}
		if geo[1].W <= before[1].W {
			t.Errorf("left pane did not widen: %d -> %d", before[1].W, geo[1].W)
		}
	})
}
