package layout

import (
	"math"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// The arrangements, under tmux's names.
const (
	ArrangeTiled          = "tiled"
	ArrangeEvenHorizontal = "even-horizontal"
	ArrangeEvenVertical   = "even-vertical"
)

// Arrangements is every name ArrangeTree accepts.
var Arrangements = []string{ArrangeTiled, ArrangeEvenHorizontal, ArrangeEvenVertical}

// ArrangeTree replaces the tree's contents with windowIDs, in order, laid out
// as kind:
//
//   - even-horizontal: side by side, left to right, each as wide as the next.
//   - even-vertical: stacked, top to bottom, each as tall as the next.
//   - tiled: a grid of rows. Each row holds ceil(sqrt(n)) panes, and the last
//     row holds what is left, wider, as tmux's tiled layout does.
func (t *BSPTree) ArrangeTree(kind string, windowIDs []int) error {
	var root *TileNode
	switch kind {
	case ArrangeEvenHorizontal:
		root = evenChain(SplitVertical, leaves(windowIDs))
	case ArrangeEvenVertical:
		root = evenChain(SplitHorizontal, leaves(windowIDs))
	case ArrangeTiled:
		root = tiledGrid(windowIDs)
	default:
		return errfmt.Errorf("unknown layout %q (use tiled, even-horizontal or even-vertical)", kind)
	}
	t.Root = root
	t.WindowToNode = make(map[int]*TileNode, len(windowIDs))
	t.indexLeaves(root)
	return nil
}

func leaves(ids []int) []*TileNode {
	out := make([]*TileNode, len(ids))
	for i, id := range ids {
		out[i] = NewLeafNode(id)
	}
	return out
}

func evenChain(split SplitType, nodes []*TileNode) *TileNode {
	switch len(nodes) {
	case 0:
		return nil
	case 1:
		nodes[0].Parent = nil
		return nodes[0]
	}
	rest := evenChain(split, nodes[1:])
	return NewInternalNode(split, 1/float64(len(nodes)), nodes[0], rest)
}

func tiledGrid(ids []int) *TileNode {
	n := len(ids)
	if n == 0 {
		return nil
	}
	cols := int(math.Ceil(math.Sqrt(float64(n))))
	var rows []*TileNode
	for start := 0; start < n; start += cols {
		end := min(start+cols, n)
		rows = append(rows, evenChain(SplitVertical, leaves(ids[start:end])))
	}
	return evenChain(SplitHorizontal, rows)
}

func (t *BSPTree) indexLeaves(n *TileNode) {
	if n == nil {
		return
	}
	if n.IsLeaf() {
		t.WindowToNode[n.WindowID] = n
		return
	}
	t.indexLeaves(n.Left)
	t.indexLeaves(n.Right)
}
