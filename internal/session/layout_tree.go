package session

import (
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
)

// The BSP tree as an op.
//
// A client that reshapes a workspace's tree (a split, a close, a drag of a
// divider, a rotate, a swap) used to say so only inside its next whole-state
// push. The daemon took the tree it found there without being told it had
// changed, a push never advances Version, and every peer then had to guess
// whether the tree in a state was news or an echo of its own. Two clients
// reshaping one tree inside a round trip of each other ended on whichever push
// landed last, and a client could adopt an echo of its own older tree over the
// one it had just built.
//
// Now the client sends the tree for one workspace as MsgLayoutTree, the daemon
// applies it under mutateState, Version advances, and every client (the sender
// included) is sent the result. The client applies its own change to its local
// tree at once, so a drag never waits for the round trip. The push stops
// carrying trees.
//
// The op is the whole tree of one workspace rather than an edit, because every
// tree mutation in the client already ends in a tree, and there are a dozen of
// them. Two ops on one workspace are settled by the order they reach the
// daemon: the later one stands. Nothing is refused. The daemon sends every
// applied op to every client, the sender included, and a client drops any
// state built before its own last op landed (see PushSeen), so every client
// ends on the tree of whichever op landed last. An earlier design refused an
// op built on an older tree than another client's; a drag then lost every
// step after the first conflict, because each step carried the same old base.
//
// Leaves are named by window ID on the wire. The integers in a tree are each
// client's own (GetWindowIntID hands them out locally), so an op carries the
// window ID for every leaf, and the daemon renumbers the leaves into its own
// WindowToBSPID. A client reading a tree from the session translates the
// session's integers back to window IDs and then to its own integers.

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
}

// errLayoutTreeSame refuses an op that would not change the tree, so it does
// not advance Version and wake every client for nothing.
var errLayoutTreeSame = errors.New("layout tree op changes nothing")

// ApplyLayoutTree applies one client's tree for one workspace. It reports
// whether the tree was applied; false with a nil error means the op changed
// nothing, and the caller answers the sender with the session's state so the
// sender still hears that its op landed.
func (s *Session) ApplyLayoutTree(p *LayoutTreePayload) (bool, error) {
	if p == nil {
		return false, nil
	}
	snap, err := s.mutateStateLocked(func(state *SessionState) error {
		// Counted with the change it makes. See notePushLocked.
		s.notePushLocked(p.PushOrigin, p.PushSeq)
		if p.Workspace < 0 || (p.Workspace > state.workspaceBound() && !IsScratchWorkspace(p.Workspace)) {
			return fmt.Errorf("workspace %d is out of range", p.Workspace)
		}
		trees, ids, next, changed := placeTree(state, p.Workspace, p.Tree, p.Leaves)
		if !changed {
			return errLayoutTreeSame
		}
		state.WorkspaceTrees, state.WindowToBSPID, state.NextBSPWindowID = trees, ids, next
		// mutateStateLocked advances Version by one once this returns, so the
		// op's version is the next one.
		s.noteTreeOpLocked(state.Version+1, p.PushOrigin)
		return nil
	})
	switch {
	case errors.Is(err, errLayoutTreeSame):
		return false, nil
	case err != nil:
		return false, err
	}
	s.publishState(snap)
	return true, nil
}

// placeTree returns the state's trees, window numbering and allocator with ws
// holding tree, renumbered from the client's leaf numbers into the session's.
// It writes nothing it was given: the snapshot aliases the trees and the
// numbering (see snapshotStateLocked), so the maps are cloned before a write.
// changed is false when the result is the tree the workspace already holds.
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

// pruneDeadLeaves takes every leaf out of the state's trees whose window is not
// in the state. It runs after every daemon-side mutation. A window the daemon
// closed would otherwise stay in the session's tree until some client sent an
// op for that workspace, and a client attaching in between would read a tree
// it cannot lay out and rebuild the workspace from scratch. A client push is
// not pruned: a client that sends ops removes the leaf with an op of its own,
// and one too old to send them has its trees taken as sent, as before.
//
// A leaf the numbering does not name is taken out too, since no client can say
// which window it is. The trees are replaced, never written through, for the
// reason placeTree gives.
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

// treeKeeps reports whether keep accepts every leaf under n unchanged.
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

// sessionLeafNames names the session's leaf numbers by window ID.
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

// SetLayoutTreeOps turns the session's tree ops on or off. A change is a
// mutation, so it advances Version and reaches every attached client in one
// broadcast: every client switches between ops and trees-in-pushes at the
// same version. Setting what is already in force does nothing.
func (s *Session) SetLayoutTreeOps(on bool) {
	s.stateMu.RLock()
	same := s.treeOpsOff == !on
	s.stateMu.RUnlock()
	if same {
		return
	}
	_ = s.mutateState(func(state *SessionState) error {
		s.treeOpsOff = !on
		// Recorded as a tree op: it changes how trees travel and nothing a
		// push carries, so a push built before it has missed nothing it could
		// undo. Counted as a plain mutation, it made every push in flight
		// stale, and the reconcile reverted a minimise, a move or a rename.
		// With no origin it also reads as another client's tree op, so each
		// client's next push is answered with the state, which is right: the
		// trees are about to travel another way.
		s.noteTreeOpLocked(state.Version+1, "")
		return nil
	})
}

// noteTreeOpLocked records that the mutation which will carry version was a
// tree op. A tree op changes the trees and nothing else, and a push from a
// client that sends ops carries no trees, so a push built before a tree op
// has missed nothing it could undo. See missedMutationLocked. The caller holds
// stateMu.
func (s *Session) noteTreeOpLocked(version int, origin string) {
	s.treeOps[version%len(s.treeOps)] = treeOpRecord{version, origin}
}

// treeOpRecord is one entry of Session.treeOps.
type treeOpRecord struct {
	version int
	origin  string
}

// treeOpAt reports whether the mutation at version was a tree op, and who
// sent it.
func (s *Session) treeOpAt(version int) (string, bool) {
	r := s.treeOps[version%len(s.treeOps)]
	return r.origin, r.version == version
}

// missedPeerTreeLocked reports whether a push from origin built at base
// predates a tree op another client sent. A tree op from origin itself does
// not count: its tree is the one the client holds. The caller holds stateMu.
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

// missedMutationLocked reports whether a push built at base predates a
// mutation other than a tree op. That is what stale means: the client has not
// seen something the daemon did that its push could undo.
//
// Tree ops do not count, whoever sent them. Counting them made every push
// built before a peer's resize stale, and the reconcile then took Minimized,
// Workspace and CustomName from the daemon, which undid a minimise, a move or
// a rename the pushing client had just made. A push from a client too old to
// send ops does carry trees, and it is taken as sent whether or not it is
// stale, so skipping tree ops changes nothing for it.
//
// Versions further back than the record reaches read as missed, which is the
// safe answer. The caller holds stateMu.
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

// handleLayoutTree applies a client's tree op. An op that is applied reaches
// every client, its sender included, through the state sink. One that changes
// nothing, or that is malformed, is answered to its sender alone with the state
// that stands, so the sender still hears that its op landed and never keeps
// showing a tree the session did not take.
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
		return fmt.Errorf("invalid layout tree payload: %w", err)
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
		// A refused op still counts: the client counted it when it sent it.
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
