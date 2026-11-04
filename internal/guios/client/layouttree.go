package client

import (
	"encoding/json"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// A tuios session keeps one BSP tree per workspace (layout-tree), which
// every TUI client of the session lays its panes out by. guios shows that
// tree as kero's split tree and writes its own splits, drags, moves and
// closes back with set-layout-tree, so a TUI on the same session shows them
// at once. The master-stack and scrolling layouts keep no tree: guios shows
// them by the rectangles the TUI gave each pane (cutRects) and keeps its
// own changes to itself.

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
	useTree   bool // the tree arranges the panes, and guios's changes go back to it
	tui       bool // a TUI client is attached
	tiling    bool
	mode      string
	scheme    int
	defRatio  float64
	numWS     int
	wsNames   map[int]string
	wsOrder   []int
	zoomByTUI string // the pane a TUI zoomed on the shown workspace
}

// readTree reads the session's settings and the shown workspace's tree.
func (k *stateKeeper) readTree(name string, ws int) treeInfo {
	ti := treeInfo{ws: ws, numWS: 9}
	if raw, err := k.c.verb("session-info", map[string]any{"session": name}); err == nil {
		var si struct {
			Tiling  string            `json:"tiling_mode"`
			Mode    string            `json:"layout_mode"`
			TUI     bool              `json:"tui_attached"`
			NumWS   int               `json:"num_workspaces"`
			WSNames map[string]string `json:"workspace_names"`
			WSOrder []int             `json:"workspace_order"`
			Current int               `json:"current_workspace"`
		}
		if json.Unmarshal(raw, &si) == nil {
			ti.tiling, ti.mode, ti.tui, ti.wsOrder = si.Tiling == "tiling", si.Mode, si.TUI, si.WSOrder
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
			ti.tree = lt.Tree
			ti.leaves = make(map[int]string, len(lt.Leaves))
			for k, v := range lt.Leaves {
				if n, err := strconv.Atoi(k); err == nil {
					ti.leaves[n] = v
				}
			}
			if lt.Tree != nil {
				ti.scheme, ti.defRatio = lt.Tree.AutoScheme, lt.Tree.DefaultRatio
			}
		}
	}
	// Master-stack and scrolling place panes without the tree; a TUI with
	// tiling off floats them. With no TUI, guios is what lays them out.
	treeMode := ti.mode != "master-stack" && ti.mode != "scrolling"
	ti.useTree = treeMode && (ti.tiling || !ti.tui)
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
	tree, leaves := toTree(l, ti.scheme, ti.defRatio)
	_, err := c.verb("set-layout-tree", map[string]any{"session": name, "workspace": ti.ws, "tree": tree, "leaves": leaves})
	return err == nil
}

// layoutOf is the split tree of a session's shown panes. Where the
// workspace's tree arranges them, it is that tree, with a pane the tree
// lacks put beside the focus. A TUI places such a pane in the tree itself;
// with none attached, guios writes the tree back with the pane in it.
// Elsewhere the tree is cut from the panes' rectangles, or is the app's.
func (k *stateKeeper) layoutOf(sid, name string, wins []windowRow, focus string, ti *treeInfo) *proto.Layout {
	if !ti.useTree {
		// A TUI lays these panes out (master-stack, scrolling): its
		// rectangles say where each one is, every time.
		if ti.tui {
			if t := cutRects(wins); t != nil {
				k.mu.Lock()
				k.layouts[sid] = t
				k.mu.Unlock()
				if t.Leaf() {
					return nil
				}
				return t.Clone()
			}
		}
		return k.layoutFor(sid, wins, focus)
	}
	shown := make(map[string]bool, len(wins))
	for _, w := range wins {
		shown[w.ID] = true
	}
	var l *proto.Layout
	if ti.tree != nil {
		l = fromTree(ti.tree.Root, ti.leaves, shown)
	} else if len(wins) > 1 {
		l = k.layoutFor(sid, wins, focus)
	}
	write := ti.tree == nil && l != nil
	for _, w := range wins {
		if l == nil {
			l = &proto.Layout{Pane: w.ID}
			continue
		}
		if contains(l.Panes(), w.ID) {
			continue
		}
		at := focus
		if at == "" || !contains(l.Panes(), at) || at == w.ID {
			ids := l.Panes()
			at = ids[len(ids)-1]
		}
		l = l.Split(at, w.ID, "right", 0.5)
		write = true
	}
	if write && !ti.tui && l != nil {
		go k.c.sendTree(name, *ti, l.Clone())
	}
	if l == nil || l.Leaf() {
		return nil
	}
	k.mu.Lock()
	k.layouts[sid] = l
	k.mu.Unlock()
	return l.Clone()
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
