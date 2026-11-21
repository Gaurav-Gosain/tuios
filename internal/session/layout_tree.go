package session

import (
	"errors"
	"maps"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// LayoutTreePayload is the body of MsgLayoutTree: one workspace's BSP tree as a
// client has just shaped it.
type LayoutTreePayload struct {
	// PushOrigin and PushSeq name the op the way they name a state push, and
	// they are counted in the same sequence. That is what lets the client drop
	// every state the daemon handed out before the op landed: such a state
	// holds an older tree than the one on its screen. See PushSeen.
	PushOrigin string
	PushSeq    uint64
	Workspace  int
	// Tree is the workspace's tree, with the leaves numbered as the client
	// numbers them. Nil means the workspace has no tree.
	Tree *SerializedBSPTree
	// Leaves names the window each leaf number in Tree stands for.
	Leaves map[int]string
	// BaseVersion is set when the tree is not a change the user made but
	// this client's reading of the state at that Version: a pane it placed
	// because the session's tree did not hold it. Such a tree carries every
	// split as the client saw it then, so it is refused when another client
	// changed the workspace's tree after that Version. Taken as sent, it
	// would undo that change: a resize made while a peer opened a pane was
	// lost to the peer placing it. The sender is answered with the state,
	// and places the pane again on the tree that stands. Zero for a change
	// the user made, which is taken whatever it was built on.
	BaseVersion int `json:"base_version,omitempty"`
}

var errLayoutTreeSame = errors.New("layout tree op changes nothing")

var errLayoutTreeStale = errors.New("layout tree op predates a peer's tree")

var errLayoutTreeForeign = errors.New("layout tree op names none of the session's windows")

func layoutTreeForeignLocked(state *SessionState, p *LayoutTreePayload) bool {
	if p.Tree == nil || len(p.Leaves) == 0 || len(state.Windows) == 0 {
		return false
	}
	for _, id := range p.Leaves {
		for i := range state.Windows {
			if state.Windows[i].ID == id {
				return false
			}
		}
	}
	return true
}

// ApplyLayoutTree applies one client's tree for one workspace. It reports
// whether the tree was applied; false with a nil error means the op changed
// nothing or was refused as stale or as another session's tree, and the caller
// answers the sender with the session's state so the sender still hears that
// its op landed.
func (s *Session) ApplyLayoutTree(p *LayoutTreePayload) (bool, error) {
	if p == nil {
		return false, nil
	}
	snap, err := s.mutateStateLocked(func(state *SessionState) error {
		s.notePushLocked(p.PushOrigin, p.PushSeq)
		if p.Workspace < 0 || (p.Workspace > state.workspaceBound() && !IsScratchWorkspace(p.Workspace)) {
			return errfmt.Errorf("workspace %d is out of range", p.Workspace)
		}
		if p.BaseVersion != 0 && s.peerTreeChangedLocked(p.PushOrigin, p.Workspace, p.BaseVersion, state.Version) {
			return errLayoutTreeStale
		}
		if layoutTreeForeignLocked(state, p) {
			if p.PushOrigin == "" || s.refusedOrigin != p.PushOrigin {
				s.refusedOrigin = p.PushOrigin
				LogError("Refused a layout tree for %s: it names none of the session's windows", s.Name())
			}
			return errLayoutTreeForeign
		}
		if s.refusedOrigin == p.PushOrigin {
			s.refusedOrigin = ""
		}
		trees, ids, next, changed := placeTree(state, p.Workspace, p.Tree, p.Leaves)
		if !changed {
			return errLayoutTreeSame
		}
		state.WorkspaceTrees, state.WindowToBSPID, state.NextBSPWindowID = trees, ids, next
		s.noteTreeOpLocked(state.Version+1, p.PushOrigin, p.Workspace)
		return nil
	})
	switch {
	case errors.Is(err, errLayoutTreeSame), errors.Is(err, errLayoutTreeStale), errors.Is(err, errLayoutTreeForeign):
		return false, nil
	case err != nil:
		return false, err
	}
	s.publishState(snap)
	return true, nil
}

func placeTree(state *SessionState, ws int, tree *SerializedBSPTree, leaves map[int]string) (map[int]*SerializedBSPTree, map[string]int, int, bool) {
	live := make(map[string]bool, len(state.Windows))
	for i := range state.Windows {
		live[state.Windows[i].ID] = true
	}
	ids := maps.Clone(state.WindowToBSPID)
	if ids == nil {
		ids = make(map[string]int)
	}
	owner := make(map[int]string, len(ids))
	next := max(state.NextBSPWindowID, 1)
	for id, n := range ids {
		owner[n] = id
		next = max(next, n+1)
	}
	placed := make(map[string]bool)
	renumbered := RemapTree(tree, func(n int) (int, bool) {
		id := leaves[n]
		if id == "" || !live[id] || placed[id] {
			return 0, false
		}
		placed[id] = true
		if sn, ok := ids[id]; ok && owner[sn] == id {
			return sn, true
		}
		sn := next
		next++
		ids[id] = sn
		owner[sn] = id
		return sn, true
	})

	name := sessionLeafNames(ids)
	if TreeKey(renumbered, name) == TreeKey(state.WorkspaceTrees[ws], name) {
		return state.WorkspaceTrees, state.WindowToBSPID, state.NextBSPWindowID, false
	}
	trees := maps.Clone(state.WorkspaceTrees)
	if trees == nil {
		trees = make(map[int]*SerializedBSPTree)
	}
	if renumbered == nil {
		delete(trees, ws)
	} else {
		trees[ws] = renumbered
	}
	return trees, ids, next, true
}

func pruneDeadLeaves(state *SessionState) {
	if len(state.WorkspaceTrees) == 0 {
		return
	}
	live := make(map[string]bool, len(state.Windows))
	for i := range state.Windows {
		live[state.Windows[i].ID] = true
	}
	owner := make(map[int]string, len(state.WindowToBSPID))
	for id, n := range state.WindowToBSPID {
		owner[n] = id
	}
	keep := func(n int) (int, bool) { return n, live[owner[n]] }
	var trees map[int]*SerializedBSPTree
	for ws, tree := range state.WorkspaceTrees {
		if tree == nil || treeKeeps(tree.Root, keep) {
			continue
		}
		if trees == nil {
			trees = maps.Clone(state.WorkspaceTrees)
		}
		if pruned := RemapTree(tree, keep); pruned != nil {
			trees[ws] = pruned
		} else {
			delete(trees, ws)
		}
	}
	if trees != nil {
		state.WorkspaceTrees = trees
	}
}

func treeKeeps(n *SerializedBSPNode, keep func(int) (int, bool)) bool {
	if n == nil {
		return true
	}
	if n.Left == nil && n.Right == nil {
		m, ok := keep(n.WindowID)
		return ok && m == n.WindowID
	}
	return treeKeeps(n.Left, keep) && treeKeeps(n.Right, keep)
}

// RemapTree returns a copy of tree with every leaf renumbered by remap. A leaf
// remap refuses is taken out, and its sibling takes its parent's place, which is
// what removing a window from a BSP tree does. Nil when no leaf is left. The
// tree must have passed checkBSPTree, which bounds the recursion.
func RemapTree(tree *SerializedBSPTree, remap func(int) (int, bool)) *SerializedBSPTree {
	if tree == nil {
		return nil
	}
	root := remapNode(tree.Root, remap)
	if root == nil {
		return nil
	}
	return &SerializedBSPTree{Root: root, AutoScheme: tree.AutoScheme, DefaultRatio: tree.DefaultRatio}
}

func remapNode(n *SerializedBSPNode, remap func(int) (int, bool)) *SerializedBSPNode {
	if n == nil {
		return nil
	}
	if n.Left == nil && n.Right == nil {
		id, ok := remap(n.WindowID)
		if !ok {
			return nil
		}
		return &SerializedBSPNode{WindowID: id}
	}
	left, right := remapNode(n.Left, remap), remapNode(n.Right, remap)
	switch {
	case left == nil:
		return right
	case right == nil:
		return left
	}
	return &SerializedBSPNode{SplitType: n.SplitType, SplitRatio: n.SplitRatio, Left: left, Right: right}
}

// TreeKey renders a tree with its leaves named by window ID, so two trees
// numbered by different clients compare equal when they lay out the same
// windows the same way. name turns a leaf number into a window ID; a number it
// cannot name is rendered as the number. Nil and empty trees render as "".
func TreeKey(tree *SerializedBSPTree, name func(int) string) string {
	if tree == nil || tree.Root == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(strconv.Itoa(tree.AutoScheme))
	b.WriteByte('/')
	b.WriteString(strconv.FormatFloat(tree.DefaultRatio, 'g', -1, 64))
	b.WriteByte(' ')
	writeTreeKey(&b, tree.Root, name)
	return b.String()
}

func writeTreeKey(b *strings.Builder, n *SerializedBSPNode, name func(int) string) {
	if n == nil {
		b.WriteByte('_')
		return
	}
	if n.Left == nil && n.Right == nil {
		if id := name(n.WindowID); id != "" {
			b.WriteString(id)
		} else {
			b.WriteByte('#')
			b.WriteString(strconv.Itoa(n.WindowID))
		}
		return
	}
	b.WriteByte('(')
	b.WriteString(strconv.Itoa(n.SplitType))
	b.WriteByte('@')
	b.WriteString(strconv.FormatFloat(n.SplitRatio, 'g', -1, 64))
	b.WriteByte(' ')
	writeTreeKey(b, n.Left, name)
	b.WriteByte(' ')
	writeTreeKey(b, n.Right, name)
	b.WriteByte(')')
}

func sessionLeafNames(ids map[string]int) func(int) string {
	owner := make(map[int]string, len(ids))
	for id, n := range ids {
		owner[n] = id
	}
	return func(n int) string { return owner[n] }
}

// SessionTreeNames is sessionLeafNames for a client reading a state.
func SessionTreeNames(state *SessionState) func(int) string {
	return sessionLeafNames(state.WindowToBSPID)
}

func (s *Session) noteTreeOpLocked(version int, origin string, ws int) {
	s.treeOps[version%len(s.treeOps)] = treeOpRecord{version, origin, ws}
}

const (
	treeOpAnyWorkspace = -1
	treeOpNoWorkspace  = -2
)

type treeOpRecord struct {
	version int
	origin  string
	ws      int
}

func (s *Session) peerTreeChangedLocked(origin string, ws, base, current int) bool {
	if base >= current {
		return false
	}
	if current-base > len(s.treeOps) {
		return true
	}
	for v := base + 1; v <= current; v++ {
		r := s.treeOps[v%len(s.treeOps)]
		if r.version != v || (r.ws != ws && r.ws != treeOpAnyWorkspace) {
			continue
		}
		if r.origin != origin || origin == "" {
			return true
		}
	}
	return false
}

func (s *Session) treeOpAt(version int) (string, bool) {
	r := s.treeOps[version%len(s.treeOps)]
	return r.origin, r.version == version
}

func (s *Session) missedPeerTreeLocked(origin string, base, current int) bool {
	if base >= current || current-base > len(s.treeOps) {
		return false
	}
	for v := base + 1; v <= current; v++ {
		if by, ok := s.treeOpAt(v); ok && (by != origin || origin == "") {
			return true
		}
	}
	return false
}

func (s *Session) missedMutationLocked(base, current int) bool {
	if base >= current {
		return false
	}
	if current-base > len(s.treeOps) {
		return true
	}
	for v := base + 1; v <= current; v++ {
		if _, ok := s.treeOpAt(v); !ok {
			return true
		}
	}
	return false
}

func (d *Daemon) handleLayoutTree(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return d.replyError(cs, msg, ErrCodeNotAttached, "not attached to any session")
	}
	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.replyError(cs, msg, ErrCodeSessionNotFound, "session not found")
	}
	var p LayoutTreePayload
	if err := msg.ParsePayload(&p); err != nil {
		return errfmt.Errorf("invalid layout tree payload: %w", err)
	}
	d.notePushOrigin(cs, session, p.PushOrigin)
	nodes := 0
	var err error
	if p.Tree != nil {
		err = checkBSPTree(p.Tree.Root, &nodes)
	}
	if err == nil && len(p.Leaves) > maxBSPNodes {
		err = errLayoutTooLarge
	}
	applied := false
	if err == nil {
		applied, err = session.ApplyLayoutTree(&p)
	}
	if err != nil {
		LogError("Refused a layout tree from %s: %v", cs.clientID, err)
		session.NotePush(p.PushOrigin, p.PushSeq)
	}
	if applied {
		return nil
	}
	return d.sendMessage(cs, MsgStateSync, &StateSyncPayload{
		State:       session.GetState(),
		TriggerType: "reconcile",
	})
}
