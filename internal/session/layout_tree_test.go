package session

import "testing"

// The BSP tree as an op: see layout_tree.go.

func bspLeaf(n int) *SerializedBSPNode { return &SerializedBSPNode{WindowID: n} }

func bspSplit(kind int, ratio float64, l, r *SerializedBSPNode) *SerializedBSPNode {
	return &SerializedBSPNode{SplitType: kind, SplitRatio: ratio, Left: l, Right: r}
}

func bspTree(root *SerializedBSPNode) *SerializedBSPTree {
	return &SerializedBSPTree{Root: root, DefaultRatio: 0.5}
}

// treeSession is a session with two daemon windows, a and b.
func treeSession(t *testing.T) (*Session, string, string) {
	t.Helper()
	sess, err := NewSession("tree", &SessionConfig{Shell: "/bin/sh"}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(sess.Stop)
	a, err := sess.AddDaemonWindow("a", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	b, err := sess.AddDaemonWindow("b", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	return sess, a.ID, b.ID
}

// sessionTreeKey is the session's tree for ws with the leaves named by window.
func sessionTreeKey(sess *Session, ws int) string {
	st := sess.GetState()
	return TreeKey(st.WorkspaceTrees[ws], SessionTreeNames(st))
}

// opKey is an op's tree with the leaves named by window.
func opKey(p *LayoutTreePayload) string {
	return TreeKey(p.Tree, func(n int) string { return p.Leaves[n] })
}

func applyTree(t *testing.T, sess *Session, p *LayoutTreePayload) bool {
	t.Helper()
	applied, err := sess.ApplyLayoutTree(p)
	if err != nil {
		t.Fatalf("ApplyLayoutTree: %v", err)
	}
	return applied
}

// TestLayoutTreeOpIsVersionedAndRenumbered: an op advances Version, and the
// session holds the tree the client meant whatever numbers the client gave its
// leaves. A second client numbering the same windows its own way reaches the
// same session numbers.
func TestLayoutTreeOpIsVersionedAndRenumbered(t *testing.T) {
	sess, a, b := treeSession(t)
	v := sess.GetState().Version

	op := &LayoutTreePayload{PushOrigin: "one", BaseVersion: v, Workspace: 1,
		Tree:   bspTree(bspSplit(1, 0.3, bspLeaf(7), bspLeaf(9))),
		Leaves: map[int]string{7: a, 9: b}}
	if !applyTree(t, sess, op) {
		t.Fatal("a current op was refused")
	}
	st := sess.GetState()
	if st.Version != v+1 {
		t.Fatalf("Version = %d after an op, want %d", st.Version, v+1)
	}
	if got, want := sessionTreeKey(sess, 1), opKey(op); got != want {
		t.Fatalf("session tree %q, want %q", got, want)
	}
	ids := st.WindowToBSPID

	other := &LayoutTreePayload{PushOrigin: "two", BaseVersion: v + 1, Workspace: 1,
		Tree:   bspTree(bspSplit(2, 0.6, bspLeaf(1), bspLeaf(2))),
		Leaves: map[int]string{1: b, 2: a}}
	if !applyTree(t, sess, other) {
		t.Fatal("a current op from a second client was refused")
	}
	if got, want := sessionTreeKey(sess, 1), opKey(other); got != want {
		t.Fatalf("session tree %q, want %q", got, want)
	}
	st = sess.GetState()
	if st.WindowToBSPID[a] != ids[a] || st.WindowToBSPID[b] != ids[b] {
		t.Fatalf("the session renumbered its windows: %v, was %v", st.WindowToBSPID, ids)
	}
}

// TestLayoutTreeOpBuiltOnAnOlderTreeIsRefused: two clients reshape one tree
// from the same starting point. The first op to land stands. The second is
// refused without advancing Version, and once its client has seen the first,
// its next op lands.
func TestLayoutTreeOpBuiltOnAnOlderTreeIsRefused(t *testing.T) {
	sess, a, b := treeSession(t)
	v := sess.GetState().Version

	first := &LayoutTreePayload{PushOrigin: "one", BaseVersion: v, Workspace: 1,
		Tree: bspTree(bspSplit(1, 0.3, bspLeaf(1), bspLeaf(2))), Leaves: map[int]string{1: a, 2: b}}
	second := &LayoutTreePayload{PushOrigin: "two", BaseVersion: v, Workspace: 1,
		Tree: bspTree(bspSplit(2, 0.7, bspLeaf(1), bspLeaf(2))), Leaves: map[int]string{1: a, 2: b}}
	if !applyTree(t, sess, first) {
		t.Fatal("the first op was refused")
	}
	if applyTree(t, sess, second) {
		t.Fatal("an op built before another client's op on the same tree was applied")
	}
	if got := sess.GetState().Version; got != v+1 {
		t.Fatalf("a refused op moved Version to %d", got)
	}
	if got, want := sessionTreeKey(sess, 1), opKey(first); got != want {
		t.Fatalf("session tree %q, want the first op's %q", got, want)
	}

	// Another workspace is not in conflict.
	elsewhere := *second
	elsewhere.Workspace = 2
	if !applyTree(t, sess, &elsewhere) {
		t.Fatal("an op on another workspace was refused")
	}

	second.BaseVersion = sess.GetState().Version
	if !applyTree(t, sess, second) {
		t.Fatal("an op built on the current tree was refused")
	}
}

// TestLayoutTreeOpsFromOneClientStack: a drag sends one op per step, each
// before the answer to the last is back. The client's own ops never make its
// next one stale.
func TestLayoutTreeOpsFromOneClientStack(t *testing.T) {
	sess, a, b := treeSession(t)
	v := sess.GetState().Version
	for i, ratio := range []float64{0.4, 0.45, 0.5, 0.55} {
		op := &LayoutTreePayload{PushOrigin: "one", BaseVersion: v, Workspace: 1,
			Tree: bspTree(bspSplit(1, ratio, bspLeaf(1), bspLeaf(2))), Leaves: map[int]string{1: a, 2: b}}
		if !applyTree(t, sess, op) {
			t.Fatalf("step %d of the drag was refused", i)
		}
	}
	if got := sess.GetState().WorkspaceTrees[1].Root.SplitRatio; got != 0.55 {
		t.Fatalf("ratio %.2f after the drag, want 0.55", got)
	}
}

// TestLayoutTreeOpThatChangesNothingIsNotApplied: an op that restates the tree
// the session holds does not advance Version, so it wakes no client.
func TestLayoutTreeOpThatChangesNothingIsNotApplied(t *testing.T) {
	sess, a, b := treeSession(t)
	op := &LayoutTreePayload{PushOrigin: "one", BaseVersion: sess.GetState().Version, Workspace: 1,
		Tree: bspTree(bspSplit(1, 0.3, bspLeaf(1), bspLeaf(2))), Leaves: map[int]string{1: a, 2: b}}
	applyTree(t, sess, op)
	v := sess.GetState().Version
	again := *op
	again.Tree = bspTree(bspSplit(1, 0.3, bspLeaf(5), bspLeaf(6)))
	again.Leaves = map[int]string{5: a, 6: b}
	if applyTree(t, sess, &again) {
		t.Fatal("an op restating the session's tree was applied")
	}
	if got := sess.GetState().Version; got != v {
		t.Fatalf("Version moved to %d on an op that changed nothing", got)
	}
}

// TestPushAfterOwnTreeOpIsCurrent: a client sends a tree op and, before the
// answer is back, pushes a window move built on the version it had. The push is
// not stale on account of its own op, so the move stands. A push from another
// client built at that version is stale.
//
// Negative control: with the plain version comparison back in UpdateStateFrom,
// the push is reconciled and the window returns to workspace 1.
func TestPushAfterOwnTreeOpIsCurrent(t *testing.T) {
	sess, a, b := treeSession(t)
	push := clientSnapshot(sess)
	other := clientSnapshot(sess)

	op := &LayoutTreePayload{PushOrigin: "one", BaseVersion: push.BaseVersion, Workspace: 1,
		Tree: bspTree(bspSplit(1, 0.3, bspLeaf(1), bspLeaf(2))), Leaves: map[int]string{1: a, 2: b}}
	applyTree(t, sess, op)

	push.PushOrigin = "one"
	windowByID(t, push, b).Workspace = 2
	if !sess.UpdateState(push) {
		t.Error("a push was read as stale on account of its own client's tree op")
	}
	if w := windowByID(t, sess.GetState(), b); w == nil || w.Workspace != 2 {
		t.Fatalf("the window move was undone: %+v", w)
	}

	other.PushOrigin = "two"
	if sess.UpdateState(other) {
		t.Error("a push that never saw another client's tree op was taken as current")
	}
}

// TestPushWithoutTreesKeepsTheSessionsTrees: a client that sends its trees as
// ops leaves them out of its push, and the push must not wipe them. A push
// from a client too old for ops carries trees, and they are taken as sent.
func TestPushWithoutTreesKeepsTheSessionsTrees(t *testing.T) {
	sess, a, b := treeSession(t)
	op := &LayoutTreePayload{PushOrigin: "one", BaseVersion: sess.GetState().Version, Workspace: 1,
		Tree: bspTree(bspSplit(1, 0.3, bspLeaf(1), bspLeaf(2))), Leaves: map[int]string{1: a, 2: b}}
	applyTree(t, sess, op)
	want := sessionTreeKey(sess, 1)

	push := clientSnapshot(sess)
	push.WorkspaceTrees, push.WindowToBSPID, push.NextBSPWindowID = nil, nil, 0
	sess.UpdateState(push)
	if got := sessionTreeKey(sess, 1); got != want {
		t.Fatalf("a push without trees changed the session's tree to %q, want %q", got, want)
	}

	legacy := clientSnapshot(sess)
	legacy.WorkspaceTrees = map[int]*SerializedBSPTree{1: bspTree(bspSplit(2, 0.8, bspLeaf(40), bspLeaf(41)))}
	legacy.WindowToBSPID = map[string]int{a: 40, b: 41}
	legacy.NextBSPWindowID = 42
	sess.UpdateState(legacy)
	if got, want := sessionTreeKey(sess, 1), TreeKey(legacy.WorkspaceTrees[1], SessionTreeNames(legacy)); got != want {
		t.Fatalf("an older client's tree was not taken: %q, want %q", got, want)
	}
}

// TestClosedWindowLeavesTheSessionsTree: a window the daemon closes leaves the
// session's tree at once, so a client attaching before anybody sends another op
// is not handed a leaf it cannot lay out.
func TestClosedWindowLeavesTheSessionsTree(t *testing.T) {
	sess, a, b := treeSession(t)
	op := &LayoutTreePayload{PushOrigin: "one", BaseVersion: sess.GetState().Version, Workspace: 1,
		Tree: bspTree(bspSplit(1, 0.3, bspLeaf(1), bspLeaf(2))), Leaves: map[int]string{1: a, 2: b}}
	applyTree(t, sess, op)
	if _, err := sess.CloseDaemonWindow(a); err != nil {
		t.Fatalf("CloseDaemonWindow: %v", err)
	}
	if got := sessionTreeKey(sess, 1); got != "0/0.5 "+b {
		t.Fatalf("session tree after the close = %q, want only %s", got, b)
	}

	// A leaf for a window the session does not hold is left out of an op too.
	stale := &LayoutTreePayload{PushOrigin: "one", BaseVersion: sess.GetState().Version, Workspace: 1,
		Tree: bspTree(bspSplit(2, 0.4, bspLeaf(1), bspLeaf(2))), Leaves: map[int]string{1: a, 2: b}}
	applyTree(t, sess, stale)
	if got := sessionTreeKey(sess, 1); got != "0/0.5 "+b {
		t.Fatalf("an op put a closed window back in the tree: %q", got)
	}
}
