package layout_test

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// checkTree asserts the structural invariants: every internal node has two
// children, every leaf is the one WindowToNode points at, and nothing is
// stranded off the root. A tree that fails these renders panes at stale sizes or
// drops them from the layout entirely.
func checkTree(t *testing.T, tree *layout.BSPTree, what string) {
	t.Helper()

	leaves := map[int]*layout.TileNode{}
	var walk func(n, parent *layout.TileNode, depth int)
	walk = func(n, parent *layout.TileNode, depth int) {
		if n == nil || depth > 64 {
			t.Fatalf("%s: nil node or cycle at depth %d", what, depth)
		}
		if n.Parent != parent {
			t.Errorf("%s: node %d has the wrong parent", what, n.ID)
		}
		if n.IsLeaf() {
			if prev, dup := leaves[n.WindowID]; dup {
				t.Errorf("%s: window %d appears twice (nodes %d and %d)", what, n.WindowID, prev.ID, n.ID)
			}
			leaves[n.WindowID] = n
			return
		}
		if n.Left == nil || n.Right == nil {
			t.Fatalf("%s: internal node %d has a nil child", what, n.ID)
		}
		walk(n.Left, n, depth+1)
		walk(n.Right, n, depth+1)
	}
	if tree.Root != nil {
		walk(tree.Root, nil, 0)
	}

	if len(leaves) != len(tree.WindowToNode) {
		t.Errorf("%s: tree holds %d windows, the index holds %d", what, len(leaves), len(tree.WindowToNode))
	}
	for id, node := range tree.WindowToNode {
		leaf, ok := leaves[id]
		if !ok {
			t.Errorf("%s: the index knows window %d but the tree does not", what, id)
			continue
		}
		if leaf != node {
			t.Errorf("%s: the index points window %d at a node that is not in the tree", what, id)
		}
	}
}

// checkPartition asserts the rects exactly tile bounds: inside it, positive,
// non-overlapping, and leaving nothing uncovered. Overlap alone is too weak a
// check, since a layout that loses a pane's space passes it.
func checkPartition(t *testing.T, rects map[int]layout.Rect, bounds layout.Rect, what string) {
	t.Helper()

	covered := map[[2]int]int{}
	for id, r := range rects {
		if r.W <= 0 || r.H <= 0 {
			t.Errorf("%s: window %d has size %dx%d", what, id, r.W, r.H)
			continue
		}
		if r.X < bounds.X || r.Y < bounds.Y || r.X+r.W > bounds.X+bounds.W || r.Y+r.H > bounds.Y+bounds.H {
			t.Errorf("%s: window %d at %+v escapes %+v", what, id, r, bounds)
			continue
		}
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				if other, taken := covered[[2]int{x, y}]; taken {
					t.Fatalf("%s: windows %d and %d both cover (%d,%d)", what, other, id, x, y)
				}
				covered[[2]int{x, y}] = id
			}
		}
	}
	if len(rects) > 0 && len(covered) != bounds.W*bounds.H {
		t.Errorf("%s: the panes cover %d cells of %d, leaving a gap", what, len(covered), bounds.W*bounds.H)
	}
}

// splitStep is one leader+- or leader+| on a given pane, the way the app issues
// it: the new window is inserted next to the focused one.
type splitStep struct {
	target int
	dir    layout.PreselectionDir
}

// buildSplits replays a sequence of splits from a single window, exactly as
// SplitFocusedHorizontal/Vertical do.
func buildSplits(bounds layout.Rect, steps []splitStep) *layout.BSPTree {
	tree := layout.NewBSPTree()
	tree.InsertWindow(1, 0, layout.SplitNone, 0.5, bounds)
	for i, s := range steps {
		tree.InsertWindowWithPreselection(i+2, s.target, s.dir, bounds)
	}
	return tree
}

// TestUnevenSplitsSurviveEveryCloseOrder is the reported case: build lopsided
// nested layouts with the split keys, drag the dividers off centre, then close
// the panes in every order. After each close the tree must still be a tree and
// the survivors must still tile the content region exactly.
func TestUnevenSplitsSurviveEveryCloseOrder(t *testing.T) {
	bounds := layout.Rect{X: 0, Y: 0, W: 200, H: 60}

	down, right := layout.PreselectionDown, layout.PreselectionRight
	layouts := map[string][]splitStep{
		// A vertical split, then a horizontal one inside the right child, then
		// another inside that: the "unconventional" shape from the report.
		"nested right":  {{1, right}, {2, down}, {3, right}},
		"nested left":   {{1, right}, {1, down}, {3, right}},
		"stairs":        {{1, down}, {2, right}, {3, down}, {4, right}},
		"all vertical":  {{1, right}, {2, right}, {3, right}},
		"deep one side": {{1, right}, {2, down}, {3, down}, {4, down}},
		"balanced":      {{1, right}, {1, down}, {2, down}},
	}

	for name, steps := range layouts {
		t.Run(name, func(t *testing.T) {
			n := len(steps) + 1
			for _, ratio := range []float64{0.5, 0.2, 0.8} {
				for victim := 1; victim <= n; victim++ {
					what := fmt.Sprintf("%s ratio=%.1f close=%d", name, ratio, victim)

					tree := buildSplits(bounds, steps)
					skewRatios(tree, ratio)
					checkTree(t, tree, what+" (before)")
					checkPartition(t, tree.ApplyLayout(bounds), bounds, what+" (before)")

					// Close every pane, starting at victim and wrapping, so each
					// layout is torn down in n different orders.
					for k := range n {
						id := (victim-1+k)%n + 1
						tree.RemoveWindow(id)
						step := fmt.Sprintf("%s after closing %d", what, id)
						checkTree(t, tree, step)
						checkPartition(t, tree.ApplyLayout(bounds), bounds, step)
					}
					if tree.Root != nil {
						t.Errorf("%s: closing every pane left a tree behind", what)
					}
				}
			}
		})
	}
}

// skewRatios walks every split off centre so a redistribution bug has something
// to lose. Alternating the direction keeps sibling splits from cancelling out.
func skewRatios(tree *layout.BSPTree, ratio float64) {
	i := 0
	var walk func(n *layout.TileNode)
	walk = func(n *layout.TileNode) {
		if n == nil || n.IsLeaf() {
			return
		}
		if i%2 == 0 {
			n.SplitRatio = ratio
		} else {
			n.SplitRatio = 1 - ratio
		}
		i++
		walk(n.Left)
		walk(n.Right)
	}
	walk(tree.Root)
}
