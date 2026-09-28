package app

import (
	"maps"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The BSP trees as ops. See layout_tree.go in the session package for the
// daemon's half.
//
// This client changes its own trees at once, wherever it changes them, so a
// drag or a split never waits for the daemon. What it tells the daemon is
// worked out at the one place it speaks, SyncStateToDaemon: every workspace
// whose tree is not the tree the session was last known to hold goes out as an
// op. treeSeen is that record, one key per workspace (see session.TreeKey).
//
// The same record decides what a state from the daemon may do to a tree. A
// tree that differs from treeSeen is a change this client has made and not yet
// sent, and nothing the daemon sends can be newer than it, so it stays. Any
// other tree is replaced by the session's. A state built before this client's
// own ops landed never gets that far: the ops are counted as pushes, and
// AcceptState drops a state that predates one.
//
// One kind of change is neither sent nor kept: a tree this client reworked
// while applying a state, because the session's tree did not fit what it
// holds (a pane the session's tree has not heard of yet, say). That is a local
// display decision, the same thing applyingPeerSync keeps out of the push, and
// sending it is how two clients that disagree end up arguing. treeDerived
// records it, so it is shown here, not sent, and the session's next tree
// replaces it. Two exceptions: the answer a sync owes (a pane this client
// placed), which SyncStateToDaemon sends with the trees it touched, and a tree
// for a workspace the session holds none for, where there is nothing to argue
// with.
//
// The panes are laid out from the tree this client holds, never from another
// client's rectangles. See bspRectsOffTree.

// treeOpsOn reports whether this client sends its trees as ops. Against a
// daemon that predates them it sends them in its pushes, as before.
func (m *OS) treeOpsOn() bool {
	return m.IsDaemonSession && m.DaemonClient != nil && m.DaemonClient.LayoutTreeOps()
}

// localLeafNames names this client's leaf numbers by window ID.
func (m *OS) localLeafNames() func(int) string {
	owner := make(map[int]string, len(m.WindowToBSPID))
	for id, n := range m.WindowToBSPID {
		owner[n] = id
	}
	return func(n int) string { return owner[n] }
}

// wireTree is a local tree in the session's serialized form, still numbered
// with this client's leaf numbers. Nil for a nil or empty tree.
func wireTree(tree *layout.BSPTree) *session.SerializedBSPTree {
	if tree == nil || tree.IsEmpty() {
		return nil
	}
	s := tree.Serialize()
	return &session.SerializedBSPTree{
		Root:         convertBSPNode(s.Root),
		AutoScheme:   s.AutoScheme,
		DefaultRatio: s.DefaultRatio,
	}
}

// treeKeys keys every tree this client holds, by workspace.
func (m *OS) treeKeys() map[int]string {
	name := m.localLeafNames()
	keys := make(map[int]string, len(m.WorkspaceTrees))
	for ws, tree := range m.WorkspaceTrees {
		if k := session.TreeKey(wireTree(tree), name); k != "" {
			keys[ws] = k
		}
	}
	return keys
}

// noteSessionTrees records the trees this client now holds as the session's.
// It is for an attach, where the trees were just taken from the session whole.
func (m *OS) noteSessionTrees() {
	m.treeSeen = m.treeKeys()
	m.treeDerived = nil
}

// unsentTree reports whether key, a tree this client holds for ws, is a change
// of its own it has not sent.
func (m *OS) unsentTree(ws int, key string) bool {
	return key != m.treeSeen[ws] && key != m.treeDerived[ws]
}

// unsentTrees lists the workspaces whose tree is a change this client has not
// sent.
func (m *OS) unsentTrees() map[int]bool {
	out := make(map[int]bool)
	for ws, key := range m.treeKeysWithEmpty() {
		if m.unsentTree(ws, key) {
			out[ws] = true
		}
	}
	return out
}

// noteDerivedTrees marks every tree that applying a state changed as a local
// derivation (see treeDerived). unsentBefore is unsentTrees from before the
// state was applied: a change the user made stays a change to send.
func (m *OS) noteDerivedTrees(unsentBefore map[int]bool) {
	for ws, key := range m.treeKeysWithEmpty() {
		if unsentBefore[ws] || !m.unsentTree(ws, key) {
			continue
		}
		// Where the session holds no tree at all there is nothing to argue
		// with, and a tree nobody sends is a tree no two clients share. It
		// stays a change to send.
		if m.treeSeen[ws] == "" {
			continue
		}
		if m.treeDerived == nil {
			m.treeDerived = make(map[int]string)
		}
		m.treeDerived[ws] = key
	}
}

// treeKeysWithEmpty is treeKeys with an empty key for every workspace the
// session was known to hold a tree for and this client holds none.
func (m *OS) treeKeysWithEmpty() map[int]string {
	keys := m.treeKeys()
	for ws := range m.treeSeen {
		if _, ok := keys[ws]; !ok {
			keys[ws] = ""
		}
	}
	return keys
}

// sendTreeOps sends every tree this client has changed since the session last
// held it. It reports false when an op could not be sent, and the tree stays
// marked as unsent so the next sync tries again.
func (m *OS) sendTreeOps() bool {
	if !m.AutoTiling {
		return true
	}
	if m.treeSeen == nil {
		m.treeSeen = make(map[int]string)
	}
	name := m.localLeafNames()
	workspaces := slices.Sorted(maps.Keys(m.WorkspaceTrees))
	for ws := range m.treeSeen {
		if _, ok := m.WorkspaceTrees[ws]; !ok {
			workspaces = append(workspaces, ws)
		}
	}
	for _, ws := range workspaces {
		tree := wireTree(m.WorkspaceTrees[ws])
		key := session.TreeKey(tree, name)
		if !m.unsentTree(ws, key) {
			continue
		}
		var leaves map[int]string
		if tree != nil {
			leaves = make(map[int]string)
			collectLeaves(tree.Root, name, leaves)
		}
		if err := m.DaemonClient.SendLayoutTree(ws, tree, leaves, m.DaemonStateVersion); err != nil {
			m.LogError("Failed to send the layout of workspace %d to the daemon: %v", ws, err)
			return false
		}
		if key == "" {
			delete(m.treeSeen, ws)
		} else {
			m.treeSeen[ws] = key
		}
		delete(m.treeDerived, ws)
	}
	return true
}

func collectLeaves(n *session.SerializedBSPNode, name func(int) string, into map[int]string) {
	if n == nil {
		return
	}
	if n.Left == nil && n.Right == nil {
		if id := name(n.WindowID); id != "" {
			into[n.WindowID] = id
		}
		return
	}
	collectLeaves(n.Left, name, into)
	collectLeaves(n.Right, name, into)
}

// bspRectsOffTree reports whether a tiled pane on screen is not where the tree
// of the workspace on screen puts it.
//
// A state carries the rectangles of whichever client pushed last, and with the
// tree sent as an op, those can be rectangles laid out from a tree the session
// has since replaced: the push and the op that superseded it cross on the
// wire. A client that adopted them would show one tree's layout while holding
// another tree. The staleness check cannot see it, because the rectangles
// still fill the box. Laying the panes out again from this client's own tree
// is idempotent when they already match, which is the usual case.
func (m *OS) bspRectsOffTree() bool {
	if !m.AutoTiling || !m.UseBSPLayout || m.UseScrollingLayout || m.zoomedWindow() != nil {
		return false
	}
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil || tree.IsEmpty() {
		return false
	}
	layouts := tree.ApplyLayout(m.GetBSPBounds(), m.separatorGap())
	for _, w := range m.Windows {
		if w == nil || w.Workspace != m.CurrentWorkspace || w.Minimized || w.IsFloating {
			continue
		}
		rect, ok := layouts[m.GetWindowIntID(w.ID)]
		if !ok {
			return true
		}
		x, y, width, height := m.dragSlotOf(w)
		// A pane on its way somewhere is judged by where it is going.
		for _, anim := range m.Animations {
			if anim != nil && anim.Window == w && !anim.Complete {
				x, y, width, height = anim.EndX, anim.EndY, anim.EndWidth, anim.EndHeight
			}
		}
		if x != rect.X || y != rect.Y || width != rect.W || height != rect.H {
			return true
		}
	}
	return false
}

// adoptSessionTrees takes the session's trees from a state the daemon sent,
// except where this client has a change of its own it has not sent yet.
//
// The session numbers its leaves its own way, so each leaf is read as a window
// ID through the state's numbering and then given this client's number for that
// window. A leaf for a window this client does not hold is left out.
//
// A tree that already matches is left alone rather than rebuilt, so a drag in
// progress keeps the nodes it is holding when the answer to its own op arrives.
//
// It reports whether the tree of the workspace on screen was replaced. The
// rectangles on screen were laid out from the tree they replace, and nothing
// else in the sync can tell: they still fill the box, so the staleness check
// passes them. The caller retiles.
func (m *OS) adoptSessionTrees(state *session.SessionState) (replacedOnScreen bool) {
	if !state.AutoTiling {
		return false
	}
	if m.treeSeen == nil {
		m.treeSeen = make(map[int]string)
	}
	present := make(map[string]bool, len(m.Windows))
	for _, w := range m.Windows {
		present[w.ID] = true
	}
	sessionName := session.SessionTreeNames(state)
	toLocal := func(n int) (int, bool) {
		id := sessionName(n)
		if id == "" || !present[id] {
			return 0, false
		}
		return m.GetWindowIntID(id), true
	}

	workspaces := make(map[int]bool)
	for ws := range state.WorkspaceTrees {
		workspaces[ws] = true
	}
	for ws := range m.WorkspaceTrees {
		workspaces[ws] = true
	}
	for ws := range m.treeSeen {
		workspaces[ws] = true
	}
	for _, ws := range slices.Sorted(maps.Keys(workspaces)) {
		name := m.localLeafNames()
		local := session.TreeKey(wireTree(m.WorkspaceTrees[ws]), name)
		if m.unsentTree(ws, local) {
			continue // this client's own change, on its way to the daemon
		}
		delete(m.treeDerived, ws)
		wire := session.RemapTree(state.WorkspaceTrees[ws], toLocal)
		// GetWindowIntID may have numbered a window just now, so the names
		// are read again.
		key := session.TreeKey(wire, m.localLeafNames())
		if key == "" {
			delete(m.treeSeen, ws)
		} else {
			m.treeSeen[ws] = key
		}
		if key == local {
			continue
		}
		if ws == m.CurrentWorkspace {
			replacedOnScreen = true
		}
		if m.WorkspaceTrees == nil {
			m.WorkspaceTrees = make(map[int]*layout.BSPTree)
		}
		if wire == nil {
			m.WorkspaceTrees[ws] = nil
			continue
		}
		m.WorkspaceTrees[ws] = (&layout.SerializedBSPTree{
			Root:         convertSessionBSPNode(wire.Root),
			AutoScheme:   wire.AutoScheme,
			DefaultRatio: wire.DefaultRatio,
		}).Deserialize()
	}
	return replacedOnScreen
}
