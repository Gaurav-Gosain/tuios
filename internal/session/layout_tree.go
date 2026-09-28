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
// them. Two ops built from the same tree are a real conflict, and the daemon
// settles it by version: an op built before another client's op on the same
// workspace landed is refused, and the sender is sent the tree that won. It
// adopts it, and the next change it makes is built on it.
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
	// BaseVersion is the daemon Version the client had applied when it built
	// the tree. See Session.ApplyLayoutTree.
	BaseVersion int
	Workspace   int
	// Tree is the workspace's tree, with the leaves numbered as the client
	// numbers them. Nil means the workspace has no tree.
	Tree *SerializedBSPTree
	// Leaves names the window each leaf number in Tree stands for.
	Leaves map[int]string
}

// errLayoutTreeStale refuses an op built before another client's op on the same
// workspace landed.
var errLayoutTreeStale = errors.New("layout tree op predates a newer tree")

// errLayoutTreeSame refuses an op that would not change the tree, so it does
// not advance Version and wake every client for nothing.
var errLayoutTreeSame = errors.New("layout tree op changes nothing")

// treeChange records the op that last changed one workspace's tree.
type treeChange struct {
	version int
	origin  string
}

// ApplyLayoutTree applies one client's tree for one workspace. It reports
// whether the tree was applied; false with a nil error means it was refused as
// stale or as a no-op, and the caller answers the sender with the session's
// state so it converges on the tree that stands.
//
// An op is stale when another client's op changed the tree of the same
// workspace after the version the op was built from. The sender's own earlier
// ops never make it stale: a drag sends one op per step, each built on the
// previous one, faster than the answers come back. An op with no origin cannot
// be told from another client's, so it is held to the stricter rule.
func (s *Session) ApplyLayoutTree(p *LayoutTreePayload) (bool, error) {
	if p == nil {
		return false, nil
	}
	snap, err := s.mutateStateLocked(func(state *SessionState) error {
		if p.Workspace < 0 || p.Workspace > state.workspaceBound() {
			return fmt.Errorf("workspace %d is out of range", p.Workspace)
		}
		if c, ok := s.treeChanged[p.Workspace]; ok && c.version > p.BaseVersion && (c.origin != p.PushOrigin || p.PushOrigin == "") {
			return errLayoutTreeStale
		}
		trees, ids, next, changed := placeTree(state, p.Workspace, p.Tree, p.Leaves)
		if !changed {
			return errLayoutTreeSame
		}
		state.WorkspaceTrees, state.WindowToBSPID, state.NextBSPWindowID = trees, ids, next
		// mutateStateLocked advances Version by one once this returns, so the
		// op's version is the next one.
		s.noteOwnMutationLocked(state.Version+1, p.PushOrigin)
		if s.treeChanged == nil {
			s.treeChanged = make(map[int]treeChange)
		}
		s.treeChanged[p.Workspace] = treeChange{state.Version + 1, p.PushOrigin}
		return nil
	})
	switch {
	case errors.Is(err, errLayoutTreeStale), errors.Is(err, errLayoutTreeSame):
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

// noteOwnMutationLocked records that the mutation which will carry version was
// made by the client connection origin. A push from that client built before it
// is not stale on its account: the client made the change and already shows
// it. See missedMutationLocked. The caller holds stateMu.
func (s *Session) noteOwnMutationLocked(version int, origin string) {
	if origin == "" {
		return
	}
	if s.opVersions == nil {
		s.opVersions = make(map[int]string)
	}
	s.opVersions[version] = origin
	delete(s.opVersions, version-maxOwnMutations)
}

// maxOwnMutations bounds the record noteOwnMutationLocked keeps. A push built
// further back than this is read as stale, which is the safe answer.
const maxOwnMutations = 1024

// missedMutationLocked reports whether a push from origin built at base
// predates a mutation that origin did not make itself. That is what stale
// means: the client has not seen something the daemon did. Its own ops do not
// count, or every push after a tree op would be reconciled as stale until the
// op's answer came back, and a window move pushed in that gap would be undone.
// The caller holds stateMu.
func (s *Session) missedMutationLocked(origin string, base, current int) bool {
	if base >= current {
		return false
	}
	if origin == "" || current-base > maxOwnMutations {
		return true
	}
	for v := base + 1; v <= current; v++ {
		if s.opVersions[v] != origin {
			return true
		}
	}
	return false
}

// handleLayoutTree applies a client's tree op. An op that is applied reaches
// every client, its sender included, through the state sink. One that is
// refused is answered to its sender alone with the state that stands, so the
// sender never keeps showing a tree the session did not take.
func (d *Daemon) handleLayoutTree(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}
	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}
	var p LayoutTreePayload
	if err := msg.ParsePayload(&p); err != nil {
		return fmt.Errorf("invalid layout tree payload: %w", err)
	}
	d.notePush(cs, session, p.PushOrigin, p.PushSeq)
	if p.PushOrigin != "" && len(p.PushOrigin) > maxPushOriginLen {
		p.PushOrigin = ""
	}
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
	}
	if applied {
		return nil
	}
	return d.sendMessage(cs, MsgStateSync, &StateSyncPayload{
		State:       session.GetState(),
		TriggerType: "reconcile",
	})
}
