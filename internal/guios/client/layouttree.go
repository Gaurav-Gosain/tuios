package client

import (
	"slices"
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
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

// treeInfo is what the session says about the shown workspace's layout.
type treeInfo struct {
	ws        int
	tree      *session.SerializedBSPTree
	leaves    map[int]string
	ls        layoutState
	grid      layout.Rect // guios's pane area in cells (setGrid)
	useTree   bool        // the tree arranges the panes, and guios's changes go back to it
	tui       bool        // a TUI client is attached
	numWS     int
	wsNames   map[int]string
	wsOrder   []int
	zoomByTUI string // the pane a TUI zoomed on the shown workspace
}

// readTree reads the session's settings and the shown workspace's tree.
func (k *stateKeeper) readTree(name string, ws int) treeInfo {
	ti := treeInfo{ws: ws, numWS: 9, grid: currentGrid()}
	if raw, err := k.c.verb("session-info", map[string]any{"session": name}); err == nil {
		var si struct {
			TUI     bool              `json:"tui_attached"`
			NumWS   int               `json:"num_workspaces"`
			WSNames map[string]string `json:"workspace_names"`
			WSOrder []int             `json:"workspace_order"`
			Current int               `json:"current_workspace"`
		}
		if jsonx.Unmarshal(raw, &si) == nil {
			ti.tui, ti.wsOrder = si.TUI, si.WSOrder
			if si.NumWS > 0 {
				ti.numWS = si.NumWS
			}
			ti.wsNames = map[int]string{}
			for k, v := range si.WSNames {
				if n, err := strconv.Atoi(k); err == nil && v != "" {
					ti.wsNames[n] = v
				}
			}
		}
	}
	if raw, err := k.c.verb("layout-tree", map[string]any{"session": name, "workspace": ws}); err == nil {
		var lt struct {
			Tree   *session.SerializedBSPTree `json:"tree"`
			Leaves map[string]string          `json:"leaves"`
		}
		if jsonx.Unmarshal(raw, &lt) == nil {
			_ = jsonx.Unmarshal(raw, &ti.ls)
			ti.tree = lt.Tree
			ti.leaves = make(map[int]string, len(lt.Leaves))
			for k, v := range lt.Leaves {
				if n, err := strconv.Atoi(k); err == nil {
					ti.leaves[n] = v
				}
			}
		}
	}
	ti.useTree = ti.mode() == modeBSP
	return ti
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

// toTree is kero's layout as a tuios tree, its leaves numbered from 1, with
// leaves naming the window of each number. The scheme and default ratio
// are the ones the workspace's tree had, so a tree that only moves a
// divider compares as that change alone.
func toTree(l *proto.Layout, scheme int, defRatio float64) (*session.SerializedBSPTree, map[int]string) {
	if l == nil {
		return nil, map[int]string{}
	}
	leaves := map[int]string{}
	var walk func(l *proto.Layout) *session.SerializedBSPNode
	walk = func(l *proto.Layout) *session.SerializedBSPNode {
		if l.Leaf() {
			n := len(leaves) + 1
			leaves[n] = l.Pane
			return &session.SerializedBSPNode{WindowID: n}
		}
		t := splitHorizontal
		if l.Row {
			t = splitVertical
		}
		r := l.Ratio
		if r <= 0 || r >= 1 {
			r = 0.5
		}
		return &session.SerializedBSPNode{SplitType: t, SplitRatio: r, Left: walk(l.A), Right: walk(l.B)}
	}
	if defRatio <= 0 {
		defRatio = 0.5
	}
	return &session.SerializedBSPTree{Root: walk(l), AutoScheme: scheme, DefaultRatio: defRatio}, leaves
}

// writeTree sends a session's layout as its shown workspace's tree, when
// the tree arranges the workspace. It reports whether it went to the
// daemon.
func (c *Client) writeTree(sid string, l *proto.Layout) bool {
	c.st.mu.Lock()
	ti, ok := c.st.trees[sid]
	name := c.st.sessionName[sid]
	c.st.mu.Unlock()
	if !ok || name == "" {
		return false
	}
	return c.sendTree(name, ti, l)
}

func (c *Client) sendTree(name string, ti treeInfo, l *proto.Layout) bool {
	if !ti.useTree {
		return false
	}
	scheme, defRatio := 0, 0.0
	if ti.tree != nil {
		scheme, defRatio = ti.tree.AutoScheme, ti.tree.DefaultRatio
	} else {
		t, _, _ := ti.bspTree()
		scheme, defRatio = int(t.AutoScheme), t.DefaultRatio
	}
	tree, leaves := toTree(l, scheme, defRatio)
	_, err := c.verb("set-layout-tree", map[string]any{"session": name, "workspace": ti.ws, "tree": tree, "leaves": leaves})
	return err == nil
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
	if !ti.useTree {
		return nil
	}
	if ti.tui && ti.zoomByTUI != "" {
		// A TUI's zoom is a camera: below zoom_size 100 it lays the layout
		// out on a larger canvas and pans to the zoomed pane. guios draws
		// the zoom itself, the zoomed pane over the whole area, from the
		// tree, which the zoom leaves alone.
		focus = ti.zoomByTUI
	}
	t := tiled(wins)
	tree, leaves, nums := ti.bspTree()
	ids := make([]int, len(t))
	shown := make(map[string]bool, len(t))
	for i, w := range t {
		ids[i] = number(leaves, nums, w.ID)
		shown[w.ID] = true
	}
	changed := tree.PruneTo(ids)
	var missing []int
	for _, n := range ids {
		if !tree.HasWindow(n) {
			missing = append(missing, n)
		}
	}
	bounds, gap := ti.bounds(), ti.ls.Gap
	// Before guios measured its grid, bounds is a guess, and placeNew
	// decided by it. Once the grid came, the panes placed under the guess
	// go in again, in their order, by the same rule, while the tree is
	// still the one guios built: the layout no longer depends on whether
	// the grid came before the panes or after them (QM-009).
	guess := !ti.tui && (ti.grid.W < 4 || ti.grid.H < 2)
	k.mu.Lock()
	g, wasGuess := k.guessed[sid]
	if !guess {
		delete(k.guessed, sid)
	}
	k.mu.Unlock()
	restored := false
	if wasGuess && !guess && !ti.tui && g.ws == ti.ws && treeKey(tree, leaves) == g.key {
		fresh := layout.NewBSPTree()
		fresh.AutoScheme, fresh.DefaultRatio = tree.AutoScheme, tree.DefaultRatio
		for _, n := range ids {
			placeNew(fresh, n, nums[g.target], bounds, gap)
		}
		tree, restored, changed = fresh, true, true
	}
	if !restored && len(missing) == 1 && !tree.IsEmpty() {
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
			if t, ok := slot.restore(tree, leaves, missing[0]); ok {
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
		if guess {
			k.mu.Lock()
			if old, ok := k.guessed[sid]; ok && old.ws == ti.ws {
				target = old.target
			}
			k.guessed[sid] = guessedTree{ws: ti.ws, target: target, key: treeKey(tree, leaves)}
			k.mu.Unlock()
		}
	}
	if len(missing) > 0 {
		k.mu.Lock()
		delete(k.pending, sid)
		k.mu.Unlock()
	}
	if changed && !ti.tui && !tree.IsEmpty() {
		// Written before the build ends, so the next build reads it.
		_, _ = k.c.verb("set-layout-tree", map[string]any{"session": name, "workspace": ti.ws, "tree": sessionTree(tree), "leaves": leaves})
	}
	var l *proto.Layout
	if st := sessionTree(tree); st != nil {
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

// guessedTree is a tree guios placed new panes in before it measured its
// grid: its workspace, the pane they split, and the tree as guios wrote it.
type guessedTree struct {
	ws     int
	target string
	key    string
}

// treeKey is t's shape, panes and ratios, to tell whether it changed. It
// names the panes, not the leaf numbers, which the daemon renumbers.
func treeKey(t *layout.BSPTree, leaves map[int]string) string {
	st := sessionTree(t)
	if st == nil {
		return ""
	}
	return session.TreeKey(st, func(n int) string { return leaves[n] })
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
	st := sessionTree(t)
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
	return (&layout.SerializedBSPTree{Root: toLayoutNode(st.Root), AutoScheme: st.AutoScheme, DefaultRatio: st.DefaultRatio}).Deserialize(), true
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
	order := make([]int, 0, ti.numWS)
	seen := map[int]bool{}
	for _, n := range ti.wsOrder {
		if !seen[n] {
			seen[n] = true
			order = append(order, n)
		}
	}
	for n := 1; n <= max(ti.numWS, 9); n++ {
		if !seen[n] {
			seen[n] = true
			order = append(order, n)
		}
	}
	var out []proto.Workspace
	for _, n := range order {
		if w := counts[n]; w != nil {
			w.Name = ti.wsNames[n]
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
	if !have || !ti.useTree {
		return false, false
	}
	tree, leaves, nums := ti.bspTree()
	bounds, gap := ti.bounds(), ti.ls.Gap
	n := number(leaves, nums, pane)
	if !tree.HasWindow(n) {
		// A pane the tree lacks yet goes in as layoutOf puts it.
		tree.InsertMissing([]int{n}, bounds, gap)
	}
	return tree.NextSplitDirection(n, bounds, gap) == "H", true
}
