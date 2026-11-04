package client

import (
	"encoding/json"
	"strconv"
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
		if json.Unmarshal(raw, &si) == nil {
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
		if json.Unmarshal(raw, &lt) == nil {
			_ = json.Unmarshal(raw, &ti.ls)
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
// one the tree's scheme picks, and panes missing for another reason go in
// beside the last leaf (layout.AddWindow, layout.InsertMissing). A TUI
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
	if len(missing) == 1 && !tree.IsEmpty() {
		// One new pane: it splits the pane the focus was on before it came,
		// as the TUI's insertSyncedWindow does.
		target, dir := prev, layout.PreselectionNone
		if target == "" || !shown[target] || nums[target] == missing[0] {
			target = focus
		}
		if ps.at.After(time.Now().Add(-5*time.Second)) && shown[ps.target] {
			target, dir = ps.target, ps.dir
		}
		tree.AddWindow(missing[0], nums[target], dir, bounds, gap)
		changed = true
	} else if tree.InsertMissing(ids, bounds, gap) {
		changed = true
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
