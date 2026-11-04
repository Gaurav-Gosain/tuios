package session

import (
	"encoding/json"
	"strconv"
)

// layout-tree and set-layout-tree: a workspace's BSP tree for a client that
// is not tuios, such as a desktop app that draws the panes itself. The tree is
// the one the session holds (layout_tree.go), and its leaves are the
// session's numbers; leaves names the window each number stands for, which is
// what a client keeps, since the numbers are the daemon's. set-layout-tree is
// a tree op as an attached client sends one, so every attached client takes
// the tree with the state that follows.

// verbLayoutTree answers layout-tree.
func (d *Daemon) verbLayoutTree(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string `json:"session"`
		Workspace int    `json:"workspace"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	state := sess.GetState()
	ws := p.Workspace
	if ws == 0 {
		ws = state.CurrentWorkspace
	}
	tree := state.WorkspaceTrees[ws]
	leaves := map[string]string{}
	name := SessionTreeNames(state)
	if tree != nil {
		RemapTree(tree, func(n int) (int, bool) {
			if id := name(n); id != "" {
				leaves[strconv.Itoa(n)] = id
			}
			return n, true
		})
	}
	workspaces := make(map[string]int, len(state.Windows))
	for i := range state.Windows {
		workspaces[state.Windows[i].ID] = state.Windows[i].Workspace
	}
	return map[string]any{
		"type":       "layout_tree",
		"session":    sess.Name(),
		"workspace":  ws,
		"version":    state.Version,
		"tree":       tree,
		"leaves":     leaves,
		"workspaces": workspaces,
	}, nil
}

// verbSetLayoutTree answers set-layout-tree.
func (d *Daemon) verbSetLayoutTree(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string             `json:"session"`
		Workspace int                `json:"workspace"`
		Tree      *SerializedBSPTree `json:"tree"`
		Leaves    map[int]string     `json:"leaves"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	if p.Workspace == 0 {
		p.Workspace = sess.GetState().CurrentWorkspace
	}
	nodes := 0
	var err error
	if p.Tree != nil {
		err = checkBSPTree(p.Tree.Root, &nodes)
	}
	if err == nil && len(p.Leaves) > maxBSPNodes {
		err = errLayoutTooLarge
	}
	if err != nil {
		return nil, invalidParam("tree", err.Error())
	}
	applied, err := sess.ApplyLayoutTree(&LayoutTreePayload{Workspace: p.Workspace, Tree: p.Tree, Leaves: p.Leaves})
	if err != nil {
		return nil, invalidParam("workspace", err.Error())
	}
	if applied {
		// The panes' rectangles move only when a client lays them out again,
		// and with no client attached none does, so the change is raised here.
		sess.emit(SessionEvent{Type: EventLayoutChanged, Workspace: p.Workspace})
	}
	return map[string]any{"type": "layout_tree_set", "session": sess.Name(), "workspace": p.Workspace, "changed": applied}, nil
}
