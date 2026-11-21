package app

import (
	"maps"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

func (m *OS) treeOpsOn() bool {
	return m.IsDaemonSession && m.DaemonClient != nil
}

func (m *OS) localLeafNames() func(int) string {
	owner := make(map[int]string, len(m.WindowToBSPID))
	for id, n := range m.WindowToBSPID {
		owner[n] = id
	}
	return func(n int) string { return owner[n] }
}

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

func (m *OS) noteSessionTrees() {
	m.treeSeen = m.treeKeys()
	m.treeDerived = nil
}

func (m *OS) unsentTree(ws int, key string) bool {
	return key != m.treeSeen[ws] && key != m.treeDerived[ws]
}

func (m *OS) unsentTrees() map[int]bool {
	out := make(map[int]bool)
	for ws, key := range m.treeKeysWithEmpty() {
		if m.unsentTree(ws, key) {
			out[ws] = true
		}
	}
	return out
}

func (m *OS) noteDerivedTrees(unsentBefore map[int]bool) {
	for ws, key := range m.treeKeysWithEmpty() {
		if unsentBefore[ws] || !m.unsentTree(ws, key) {
			continue
		}
		if m.treeSeen[ws] == "" {
			continue
		}
		if m.treeDerived == nil {
			m.treeDerived = make(map[int]string)
		}
		m.treeDerived[ws] = key
	}
}

func (m *OS) treeKeysWithEmpty() map[int]string {
	keys := m.treeKeys()
	for ws := range m.treeSeen {
		if _, ok := keys[ws]; !ok {
			keys[ws] = ""
		}
	}
	return keys
}

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
		base := 0
		if !m.treeAnswerUser[ws] {
			base = m.treeAnswerBase
		}
		if err := m.DaemonClient.SendLayoutTree(ws, tree, leaves, base); err != nil {
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
		if m.unsentTree(ws, local) && (m.treeSeen[ws] != "" || state.WorkspaceTrees[ws] == nil) {
			continue
		}
		delete(m.treeDerived, ws)
		wire := session.RemapTree(state.WorkspaceTrees[ws], toLocal)
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
