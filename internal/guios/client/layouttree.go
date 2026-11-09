package client

import (
	"errors"
	"maps"
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
	// The panes the tree lacks go in by tuios's rules (session.PlaceMissing),
	// with what only guios knows of them: the split it asked for, the pane
	// focused before they came, and where a pane that floated tiled.
	pc := session.PlaceContext{Prev: prev}
	if ps.at.After(time.Now().Add(-5 * time.Second)) {
		pc.Asked, pc.Target, pc.Side = true, ps.target, ps.side
	}
	k.mu.Lock()
	pc.Slots = maps.Clone(k.floated)
	k.mu.Unlock()
	tree, leaves, changed, put := session.PlaceMissing(*ti, wins, focus, pc)
	if len(put) > 0 {
		// A slot is used once: the pane that went back no longer has one.
		k.mu.Lock()
		delete(k.pending, sid)
		for _, id := range put {
			delete(k.floated, id)
		}
		k.mu.Unlock()
	}
	// Only the slot of the one pane put in goes to the daemon.
	slots := pc.Slots
	pc.Slots = nil
	if len(put) == 1 {
		if slot, ok := slots[put[0]]; ok {
			pc.Slots = map[string]session.FloatSlot{put[0]: slot}
		}
	}
	if changed && !ti.TUI && !tree.IsEmpty() {
		// The daemon places them in the tree it holds and writes it, so a
		// peer's change made meanwhile is kept (issue #162); a daemon from
		// before arrange gets the tree written whole, as before. Done before
		// the build ends, so the next build reads it.
		k.placeInDaemon(name, ti, pc, tree, leaves)
	}
	shown := make(map[string]bool, len(wins))
	for _, w := range tiled(wins) {
		shown[w.ID] = true
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

// pendingSplit is a split guios asked the daemon for and has not seen the
// pane of yet: the pane it splits and toward which side, as the TUI's
// pendingSplitTarget and pendingSplitDir.
type pendingSplit struct {
	target string
	side   string
	at     time.Time
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

// placeInDaemon asks the daemon to put the panes its tree lacks in, with what
// guios knows of them (pc). A daemon from before arrange gets tree, guios's
// own placing, written whole.
func (k *stateKeeper) placeInDaemon(name string, ti *treeInfo, pc session.PlaceContext, tree *layout.BSPTree, leaves map[int]string) {
	if !noArrange.Load() {
		args := map[string]any{"session": name, "workspace": ti.WS, "op": "place", "target": pc.Prev}
		if pc.Asked {
			args["asked"], args["target"], args["mode"] = true, pc.Target, pc.Side
		}
		for id, slot := range pc.Slots {
			// One pane floated back: the daemon puts it where it tiled.
			args["pane"], args["slot"] = id, slot
			break
		}
		g := currentGrid()
		args["cols"], args["rows"] = g.W, g.H
		_, err := k.c.verb("arrange", args)
		var old *OldDaemonError
		if !errors.As(err, &old) {
			return
		}
		noArrange.Store(true)
	}
	_, _ = k.c.verb("set-layout-tree", map[string]any{"session": name, "workspace": ti.WS, "tree": session.SessionTree(tree), "leaves": leaves})
}
