package client

import (
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// A tuios session keeps one BSP tree per workspace (layout-tree), which
// every TUI client of the session lays its panes out by. guios shows that
// tree as kero's split tree and writes its own splits, drags, moves and
// closes back with set-layout-tree, so a TUI on the same session shows them
// at once. The other layouts are laid out by arrange.go from the rest of the
// session's layout state.

// BSP split types, as internal/layout numbers them.
const (
	splitVertical   = 1 // left and right
	splitHorizontal = 2 // top and bottom
)

// readTree reads the session's settings and the shown workspace's tree.
func (k *stateKeeper) readTree(name string, ws int) treeInfo {
	return session.ReadArrangeTree(k.c.verb, name, ws, currentGrid())
}

// fromTree is the tree as kero's layout, with only the panes in shown. A
// leaf of a pane not shown goes, and its sibling takes its room, as tuios
// removes a window from a tree.
func fromTree(n *session.SerializedBSPNode, leaves map[int]string, shown map[string]bool) *proto.Layout {
	if n == nil {
		return nil
	}
	if n.Left == nil && n.Right == nil {
		id := leaves[n.WindowID]
		if id == "" || !shown[id] {
			return nil
		}
		return &proto.Layout{Pane: id}
	}
	a, b := fromTree(n.Left, leaves, shown), fromTree(n.Right, leaves, shown)
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	r := n.SplitRatio
	if r <= 0 || r >= 1 {
		r = 0.5
	}
	return &proto.Layout{Row: n.SplitType != splitHorizontal, Ratio: r, A: a, B: b}
}

// layoutOf is the split tree of a session's shown panes, when the BSP
// layout places them. It is the workspace's tree with the panes it lacks put
// in by tuios's own rules: a new pane splits the pane that had the focus
// (or the one a split asked for), on the side the split asked for or the
// one the tree's scheme picks, unless that leaves a pane too small
// (placeNew). A TUI
// places such a pane in the tree itself; with none attached, guios writes
// the tree back with the pane in it.
func (k *stateKeeper) layoutOf(sid, name string, wins []windowRow, focus string, ti *treeInfo) *proto.Layout {
	k.mu.Lock()
	prev := k.prevFocus[sid]
	k.prevFocus[sid] = focus
	ps := k.pending[sid]
	k.mu.Unlock()
	if !ti.UseTree {
		return nil
	}
	if ti.TUI && ti.ZoomByTUI != "" {
		// A TUI's zoom is a camera: below zoom_size 100 it lays the layout
		// out on a larger canvas and pans to the zoomed pane. guios draws
		// the zoom itself, the zoomed pane over the whole area, from the
		// tree, which the zoom leaves alone.
		focus = ti.ZoomByTUI
	}
	t := tiled(wins)
	tree, leaves, nums := ti.BSPTree()
	ids := make([]int, len(t))
	shown := make(map[string]bool, len(t))
	for i, w := range t {
		ids[i] = session.LeafNumber(leaves, nums, w.ID)
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
		// A pane tiled again after it floated goes back where it tiled.
		// Split off as a new pane, it split itself: a leaf the tree lacks,
		// so the last leaf, and the person lost the layout they had
		// (QM-003).
		id := leaves[missing[0]]
		k.mu.Lock()
		slot, ok := k.floated[id]
		delete(k.floated, id)
		k.mu.Unlock()
		if ok {
			t, ok := slot.restore(tree, leaves, missing[0])
			if ok {
				tree, restored, changed = t, true, true
			}
		}
	}
	switch {
	case restored:
		// In place: no other rule moves it.
	case len(missing) == 1 && !tree.IsEmpty() && ps.at.After(time.Now().Add(-5*time.Second)) && shown[ps.target]:
		// The split guios asked for: the pane it splits, on the side it
		// asked for.
		tree.AddWindow(missing[0], nums[ps.target], ps.dir, bounds, gap)
		changed = true
	case len(missing) > 0:
		// Panes guios did not ask for, one or many, as a script or an agent
		// opens them: each goes in by placeNew, beside the pane the focus
		// was on before they came. One state used to bring one pane or
		// several by timing alone, and the two went in by different rules,
		// so the same script gave two layouts (QM-009).
		target := prev
		if target == "" || !shown[target] || slices.Contains(missing, nums[target]) {
			target = focus
		}
		for _, n := range missing {
			placeNew(tree, n, nums[target], bounds, gap)
		}
		changed = true
	}
	if len(missing) > 0 {
		k.mu.Lock()
		delete(k.pending, sid)
		k.mu.Unlock()
	}
	if changed && !ti.TUI && !tree.IsEmpty() {
		// Written before the build ends, so the next build reads it.
		_, _ = k.c.verb("set-layout-tree", map[string]any{"session": name, "workspace": ti.WS, "tree": session.SessionTree(tree), "leaves": leaves})
	}
	var l *proto.Layout
	if st := session.SessionTree(tree); st != nil {
		l = fromTree(st.Root, leaves, shown)
	}
	if l == nil || l.Leaf() {
		return nil
	}
	k.mu.Lock()
	k.layouts[sid] = l
	k.mu.Unlock()
	return l.Clone()
}

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
func placeNew(t *layout.BSPTree, num, target int, bounds layout.Rect, gap int) {
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

// floatSlot is where a pane tiled as it floated out of the split tree:
// beside the panes of the other side of its split (beside), on the split's
// first side or its second (first), left and right or top and bottom (row),
// with the split's ratio.
type floatSlot struct {
	beside []string
	row    bool
	first  bool
	ratio  float64
}

// slotOf is the slot pane tiles in, in the tree of root.
func slotOf(root *session.SerializedBSPNode, leaves map[int]string, pane string) (floatSlot, bool) {
	var found floatSlot
	ok := false
	var walk func(n *session.SerializedBSPNode)
	walk = func(n *session.SerializedBSPNode) {
		if n == nil || ok || n.Left == nil || n.Right == nil {
			return
		}
		for i, side := range []*session.SerializedBSPNode{n.Left, n.Right} {
			if side.Left == nil && side.Right == nil && leaves[side.WindowID] == pane {
				other := n.Right
				if i == 1 {
					other = n.Left
				}
				found = floatSlot{beside: paneSet(other, leaves), row: n.SplitType != splitHorizontal, first: i == 0, ratio: n.SplitRatio}
				ok = len(found.beside) > 0
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
func paneSet(n *session.SerializedBSPNode, leaves map[int]string) []string {
	var out []string
	var walk func(n *session.SerializedBSPNode)
	walk = func(n *session.SerializedBSPNode) {
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
func (s floatSlot) restore(t *layout.BSPTree, leaves map[int]string, num int) (*layout.BSPTree, bool) {
	st := session.SessionTree(t)
	if st == nil {
		return nil, false
	}
	done := false
	var walk func(n *session.SerializedBSPNode) *session.SerializedBSPNode
	walk = func(n *session.SerializedBSPNode) *session.SerializedBSPNode {
		if n == nil || done {
			return n
		}
		if slices.Equal(paneSet(n, leaves), s.beside) {
			done = true
			leaf := &session.SerializedBSPNode{WindowID: num}
			split := &session.SerializedBSPNode{SplitType: splitHorizontal, SplitRatio: s.ratio, Left: n, Right: leaf}
			if s.row {
				split.SplitType = splitVertical
			}
			if s.first {
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
	return (&layout.SerializedBSPTree{Root: session.ToLayoutNode(st.Root), AutoScheme: st.AutoScheme, DefaultRatio: st.DefaultRatio}).Deserialize(), true
}

// pendingSplit is a split guios asked the daemon for and has not seen the
// pane of yet: the pane it splits and toward which side, as the TUI's
// pendingSplitTarget and pendingSplitDir.
type pendingSplit struct {
	target string
	dir    layout.PreselectionDir
	at     time.Time
}

// preselection is a kero split side as tuios's preselection, none for "".
func preselection(side string) layout.PreselectionDir {
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

// workspaceList is a session's workspaces that hold panes, with the shown
// one, in the session's order.
func workspaceList(counts map[int]*proto.Workspace, shown int, ti treeInfo) []proto.Workspace {
	if counts[shown] == nil {
		counts[shown] = &proto.Workspace{N: shown, Status: proto.Idle}
	}
	order := make([]int, 0, ti.NumWS)
	seen := map[int]bool{}
	for _, n := range ti.WSOrder {
		if !seen[n] {
			seen[n] = true
			order = append(order, n)
		}
	}
	for n := 1; n <= max(ti.NumWS, 9); n++ {
		if !seen[n] {
			seen[n] = true
			order = append(order, n)
		}
	}
	var out []proto.Workspace
	for _, n := range order {
		if w := counts[n]; w != nil {
			w.Name = ti.WSNames[n]
			out = append(out, *w)
		}
	}
	return out
}

// statusRank orders statuses by how much they ask of the user.
func statusRank(s proto.Status) int {
	switch s {
	case proto.Waiting:
		return 4
	case proto.Failed:
		return 3
	case proto.Working:
		return 2
	case proto.Idle:
		return 1
	}
	return 0
}

func ids(wins []windowRow) []string {
	out := make([]string, len(wins))
	for i, w := range wins {
		out[i] = w.ID
	}
	return out
}

// AutoSplitDown reports which way a split that names no side splits pane:
// down (true) or to the right, as layoutOf will put the new pane in the
// workspace's tree. ok is false when the split tree does not lay the
// workspace out (master-stack, scrolling, floating), where a new pane takes
// no side of the one it splits.
func (c *Client) AutoSplitDown(sid, pane string) (down, ok bool) {
	c.st.mu.Lock()
	ti, have := c.st.trees[sid]
	c.st.mu.Unlock()
	if !have || !ti.UseTree {
		return false, false
	}
	tree, leaves, nums := ti.BSPTree()
	bounds, gap := ti.Bounds(), ti.LS.Gap
	n := session.LeafNumber(leaves, nums, pane)
	if !tree.HasWindow(n) {
		// A pane the tree lacks yet goes in as layoutOf puts it.
		tree.InsertMissing([]int{n}, bounds, gap)
	}
	return tree.NextSplitDirection(n, bounds, gap) == "H", true
}
