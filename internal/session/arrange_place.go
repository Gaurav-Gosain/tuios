package session

import (
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// Smallest terminal placeNew makes, in cells. A pane's rectangle in the
// grid holds its terminal, its padding and its header too: placeOverCols
// and placeOverRows more, as guios draws a pane at 13 px (a 74 by 25 cell
// rectangle holds a 69 by 21 terminal).
const (
	placeMinCols  = 40
	placeMinRows  = 8
	placeOverCols = 6
	placeOverRows = 4
)

// placeNew puts leaf num in t. It splits leaf target in half as the TUI
// does for a pane it did not ask for (insertSyncedWindow, issue #1228), on
// the side the tree's scheme picks, when both halves hold a terminal of
// placeMinCols by placeMinRows. When they do not, or target is not in t, it
// splits another leaf: the largest first, along its longer side first, and
// gives the panes of the row or column the new pane joins equal shares
// (evenRun), as tmux's even layouts do. Halves alone cannot give six panes
// 40 columns each in a 1440 point window; thirds can. It takes the first
// split where every pane holds such a terminal, or with none, the one whose
// narrowest pane is widest. Panes opened one after another so no longer
// cut the focused pane into slivers (QM-009).
func PlaceNew(t *layout.BSPTree, num, target int, bounds layout.Rect, gap int) {
	if t.IsEmpty() {
		t.InsertWindow(num, 0, layout.SplitNone, 0, bounds, gap)
		return
	}
	if t.HasWindow(target) {
		try := t.Serialize().Deserialize()
		try.AddWindow(num, target, layout.PreselectionNone, bounds, gap)
		rs := try.ApplyLayout(bounds, gap)
		if fits(rs[num]) && fits(rs[target]) {
			t.AddWindow(num, target, layout.PreselectionNone, bounds, gap)
			return
		}
	}
	rs := t.ApplyLayout(bounds, gap)
	order := t.GetAllWindowIDs()
	slices.SortStableFunc(order, func(a, b int) int { return rs[b].W*rs[b].H - rs[a].W*rs[a].H })
	var best *layout.BSPTree
	bestScore := [2]int{-1, -1}
	for _, id := range order {
		for _, dir := range splitsOf(rs[id]) {
			try := t.Serialize().Deserialize()
			try.InsertWindow(num, id, dir, 0.5, bounds, gap)
			try = evenRun(try, num, dir)
			score := [2]int{1 << 30, 1 << 30}
			for _, r := range try.ApplyLayout(bounds, gap) {
				score[0], score[1] = min(score[0], r.W), min(score[1], r.H)
			}
			if score[0] >= placeMinCols+placeOverCols && score[1] >= placeMinRows+placeOverRows {
				*t = *try
				return
			}
			if score[0] > bestScore[0] || score[0] == bestScore[0] && score[1] > bestScore[1] {
				best, bestScore = try, score
			}
		}
	}
	*t = *best
}

func fits(r layout.Rect) bool {
	return r.W >= placeMinCols+placeOverCols && r.H >= placeMinRows+placeOverRows
}

// splitsOf is the two ways to split r, along its longer side first. Cells
// are about twice as tall as they are wide.
func splitsOf(r layout.Rect) [2]layout.SplitType {
	if r.W >= 2*r.H {
		return [2]layout.SplitType{layout.SplitVertical, layout.SplitHorizontal}
	}
	return [2]layout.SplitType{layout.SplitHorizontal, layout.SplitVertical}
}

// evenRun gives the panes of the run of dir splits leaf num is in equal
// shares: the splits of dir joined to the one above leaf num, each pane or
// subtree of another kind in it one share.
func evenRun(t *layout.BSPTree, num int, dir layout.SplitType) *layout.BSPTree {
	st := t.Serialize()
	kind := int(dir)
	// The path from the root to leaf num.
	var path []*layout.SerializedNode
	var find func(n *layout.SerializedNode) bool
	find = func(n *layout.SerializedNode) bool {
		if n == nil {
			return false
		}
		path = append(path, n)
		if n.Left == nil && n.Right == nil && n.WindowID == num {
			return true
		}
		if find(n.Left) || find(n.Right) {
			return true
		}
		path = path[:len(path)-1]
		return false
	}
	if !find(st.Root) || len(path) < 2 {
		return t
	}
	top := len(path) - 2
	for top > 0 && int(path[top-1].SplitType) == kind {
		top--
	}
	var shares func(n *layout.SerializedNode) int
	shares = func(n *layout.SerializedNode) int {
		if n.Left == nil || n.Right == nil || int(n.SplitType) != kind {
			return 1
		}
		a, b := shares(n.Left), shares(n.Right)
		n.SplitRatio = float64(a) / float64(a+b)
		return a + b
	}
	shares(path[top])
	return st.Deserialize()
}

// FloatSlot is where a pane tiled as it floated out of the split tree:
// beside the panes of the other side of its split (beside), on the split's
// first side or its second (first), left and right or top and bottom (row),
// with the split's ratio.
type FloatSlot struct {
	Beside []string `json:"beside"`
	Row    bool     `json:"row"`
	First  bool     `json:"first"`
	Ratio  float64  `json:"ratio"`
}

// slotOf is the slot pane tiles in, in the tree of root.
func SlotOf(root *SerializedBSPNode, leaves map[int]string, pane string) (FloatSlot, bool) {
	var found FloatSlot
	ok := false
	var walk func(n *SerializedBSPNode)
	walk = func(n *SerializedBSPNode) {
		if n == nil || ok || n.Left == nil || n.Right == nil {
			return
		}
		for i, side := range []*SerializedBSPNode{n.Left, n.Right} {
			if side.Left == nil && side.Right == nil && leaves[side.WindowID] == pane {
				other := n.Right
				if i == 1 {
					other = n.Left
				}
				found = FloatSlot{Beside: paneSet(other, leaves), Row: n.SplitType != layoutSplitHorizontal, First: i == 0, Ratio: n.SplitRatio}
				ok = len(found.Beside) > 0
				return
			}
		}
		walk(n.Left)
		walk(n.Right)
	}
	walk(root)
	return found, ok
}

// paneSet is the panes of the leaves under n, sorted.
func paneSet(n *SerializedBSPNode, leaves map[int]string) []string {
	var out []string
	var walk func(n *SerializedBSPNode)
	walk = func(n *SerializedBSPNode) {
		if n == nil {
			return
		}
		if n.Left == nil && n.Right == nil {
			if id := leaves[n.WindowID]; id != "" {
				out = append(out, id)
			}
			return
		}
		walk(n.Left)
		walk(n.Right)
	}
	walk(n)
	slices.Sort(out)
	return out
}

// restore puts leaf num back in its slot: it splits the subtree that holds
// exactly the panes it tiled beside, as it did. It reports false when no
// subtree holds those panes now, as after one of them closed or moved: the
// pane then goes in by the rules for a new pane.
func (s FloatSlot) Restore(t *layout.BSPTree, leaves map[int]string, num int) (*layout.BSPTree, bool) {
	st := SessionTree(t)
	if st == nil {
		return nil, false
	}
	done := false
	var walk func(n *SerializedBSPNode) *SerializedBSPNode
	walk = func(n *SerializedBSPNode) *SerializedBSPNode {
		if n == nil || done {
			return n
		}
		if slices.Equal(paneSet(n, leaves), s.Beside) {
			done = true
			leaf := &SerializedBSPNode{WindowID: num}
			split := &SerializedBSPNode{SplitType: layoutSplitHorizontal, SplitRatio: s.Ratio, Left: n, Right: leaf}
			if s.Row {
				split.SplitType = layoutSplitVertical
			}
			if s.First {
				split.Left, split.Right = leaf, n
			}
			if split.SplitRatio <= 0 || split.SplitRatio >= 1 {
				split.SplitRatio = 0.5
			}
			return split
		}
		n.Left, n.Right = walk(n.Left), walk(n.Right)
		return n
	}
	st.Root = walk(st.Root)
	if !done {
		return nil, false
	}
	return (&layout.SerializedBSPTree{Root: ToLayoutNode(st.Root), AutoScheme: st.AutoScheme, DefaultRatio: st.DefaultRatio}).Deserialize(), true
}

// SidePreselection is a split side as tuios's preselection, none for "".
func SidePreselection(side string) layout.PreselectionDir {
	switch side {
	case "left":
		return layout.PreselectionLeft
	case "right":
		return layout.PreselectionRight
	case "up":
		return layout.PreselectionUp
	case "down":
		return layout.PreselectionDown
	}
	return layout.PreselectionNone
}

// The split kinds of a serialized tree node: left and right, top and bottom.
const (
	layoutSplitVertical   = 1
	layoutSplitHorizontal = 2
)

// PlaceContext is what a client knows of the panes a tree lacks: the pane it
// asked a split of and toward which side (Asked, Target and Side), the pane
// the focus was on before they came (Prev), and where a pane that floated
// tiled before (Slots, by pane).
type PlaceContext struct {
	Asked  bool
	Target string
	Side   string
	Prev   string
	Slots  map[string]FloatSlot
}

// PlaceMissing is the workspace's tree with the tiled panes of wins it lacks
// put in by tuios's own rules, and the panes of other leaves taken out: a
// pane that floated goes back where it tiled; the one split the client
// asked for splits its target on its side; any other goes in by PlaceNew
// beside the pane the focus was on before they came (QM-003, QM-009). It
// reports whether the tree changed, and the panes it put in.
func PlaceMissing(ti ArrangeTree, wins []WindowRow, focus string, pc PlaceContext) (*layout.BSPTree, map[int]string, bool, []string) {
	t := ArrangeTiled(wins)
	tree, leaves, nums := ti.BSPTree()
	ids := make([]int, len(t))
	shown := make(map[string]bool, len(t))
	for i, w := range t {
		ids[i] = LeafNumber(leaves, nums, w.ID)
		shown[w.ID] = true
	}
	changed := tree.PruneTo(ids)
	var missing []int
	for _, n := range ids {
		if !tree.HasWindow(n) {
			missing = append(missing, n)
		}
	}
	bounds, gap := ti.Bounds(), ti.LS.Gap
	restored := false
	if len(missing) == 1 && !tree.IsEmpty() {
		if slot, ok := pc.Slots[leaves[missing[0]]]; ok {
			if t, ok := slot.Restore(tree, leaves, missing[0]); ok {
				tree, restored, changed = t, true, true
			}
		}
	}
	switch {
	case restored:
		// In place: no other rule moves it.
	case len(missing) == 1 && !tree.IsEmpty() && pc.Asked && shown[pc.Target]:
		tree.AddWindow(missing[0], nums[pc.Target], SidePreselection(pc.Side), bounds, gap)
		changed = true
	case len(missing) > 0:
		target := pc.Prev
		if target == "" || !shown[target] || slices.Contains(missing, nums[target]) {
			target = focus
		}
		for _, n := range missing {
			PlaceNew(tree, n, nums[target], bounds, gap)
		}
		changed = true
	}
	put := make([]string, 0, len(missing))
	for _, n := range missing {
		put = append(put, leaves[n])
	}
	return tree, leaves, changed, put
}
