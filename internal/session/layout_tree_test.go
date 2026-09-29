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

	op := &LayoutTreePayload{PushOrigin: "one", Workspace: 1,
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

	other := &LayoutTreePayload{PushOrigin: "two", Workspace: 1,
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

// TestLayoutTreeOpsLandInArrivalOrder: two clients reshape one tree from the
// same starting point. Nothing is refused. The later op stands, and each op
// advances Version, so every client is sent the result.
func TestLayoutTreeOpsLandInArrivalOrder(t *testing.T) {
	sess, a, b := treeSession(t)
	v := sess.GetState().Version

	first := &LayoutTreePayload{PushOrigin: "one", Workspace: 1,
		Tree: bspTree(bspSplit(1, 0.3, bspLeaf(1), bspLeaf(2))), Leaves: map[int]string{1: a, 2: b}}
	second := &LayoutTreePayload{PushOrigin: "two", Workspace: 1,
		Tree: bspTree(bspSplit(2, 0.7, bspLeaf(1), bspLeaf(2))), Leaves: map[int]string{1: a, 2: b}}
	if !applyTree(t, sess, first) {
		t.Fatal("the first op was not applied")
	}
	if !applyTree(t, sess, second) {
		t.Fatal("the second op was not applied")
	}
	if got := sess.GetState().Version; got != v+2 {
		t.Fatalf("Version = %d after two ops, want %d", got, v+2)
	}
	if got, want := sessionTreeKey(sess, 1), opKey(second); got != want {
		t.Fatalf("session tree %q, want the later op's %q", got, want)
	}
}

// TestLayoutTreeOpsFromOneClientStack: a drag sends one op per step, each
// before the answer to the last is back. The client's own ops never make its
// next one stale.
func TestLayoutTreeOpsFromOneClientStack(t *testing.T) {
	sess, a, b := treeSession(t)
	for i, ratio := range []float64{0.4, 0.45, 0.5, 0.55} {
		op := &LayoutTreePayload{PushOrigin: "one", Workspace: 1,
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
	op := &LayoutTreePayload{PushOrigin: "one", Workspace: 1,
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

// TestPushAfterATreeOpIsCurrent: a tree op lands, from this client or a peer,
// and a push built before it arrives. The op changed only the trees, and the
// push carries none, so the push is current: the window move and the minimise
// in it stand. A push built before a mutation that is not a tree op is still
// stale.
//
// Negative control: with tree ops counted as missed mutations again (the
// plain BaseVersion < Version test, or counting only the pusher's own ops),
// the peer's push is reconciled and the window comes back from workspace 2.
func TestPushAfterATreeOpIsCurrent(t *testing.T) {
	sess, a, b := treeSession(t)
	own := clientSnapshot(sess)

	op := &LayoutTreePayload{PushOrigin: "one", Workspace: 1,
		Tree: bspTree(bspSplit(1, 0.3, bspLeaf(1), bspLeaf(2))), Leaves: map[int]string{1: a, 2: b}}
	applyTree(t, sess, op)

	own.PushOrigin = "one"
	windowByID(t, own, b).Workspace = 2
	if !sess.UpdateState(own) {
		t.Error("a push was read as stale on account of its own client's tree op")
	}
	if w := windowByID(t, sess.GetState(), b); w == nil || w.Workspace != 2 {
		t.Fatalf("the window move was undone: %+v", w)
	}

	peer := clientSnapshot(sess) // built before the next op
	op.Tree = bspTree(bspSplit(2, 0.6, bspLeaf(1), bspLeaf(2)))
	op.PushOrigin = "one"
	applyTree(t, sess, op)
	peer.PushOrigin = "two"
	windowByID(t, peer, a).Minimized = true
	if !sess.UpdateState(peer) {
		t.Error("a push was read as stale on account of a peer's tree op")
	}
	got := sess.GetState()
	if w := windowByID(t, got, a); w == nil || !w.Minimized {
		t.Fatalf("the minimise was undone by a peer's tree op: %+v", w)
	}
	if w := windowByID(t, got, b); w == nil || w.Workspace != 2 {
		t.Fatalf("the earlier move was lost: %+v", w)
	}

	stale := clientSnapshot(sess)
	stale.PushOrigin = "two"
	if err := sess.RenameDaemonWindow(a, "renamed"); err != nil {
		t.Fatal(err)
	}
	if sess.UpdateState(stale) {
		t.Error("a push built before a rename was taken as current")
	}
}

// TestPushWithoutTreesKeepsTheSessionsTrees: a client that sends its trees as
// ops leaves them out of its push, and the push must not wipe them. A push
// from a client too old for ops carries trees, and they are taken as sent.
func TestPushWithoutTreesKeepsTheSessionsTrees(t *testing.T) {
	sess, a, b := treeSession(t)
	op := &LayoutTreePayload{PushOrigin: "one", Workspace: 1,
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
	op := &LayoutTreePayload{PushOrigin: "one", Workspace: 1,
		Tree: bspTree(bspSplit(1, 0.3, bspLeaf(1), bspLeaf(2))), Leaves: map[int]string{1: a, 2: b}}
	applyTree(t, sess, op)
	if _, err := sess.CloseDaemonWindow(a); err != nil {
		t.Fatalf("CloseDaemonWindow: %v", err)
	}
	if got := sessionTreeKey(sess, 1); got != "0/0.5 "+b {
		t.Fatalf("session tree after the close = %q, want only %s", got, b)
	}

	// A leaf for a window the session does not hold is left out of an op too.
	stale := &LayoutTreePayload{PushOrigin: "one", Workspace: 1,
		Tree: bspTree(bspSplit(2, 0.4, bspLeaf(1), bspLeaf(2))), Leaves: map[int]string{1: a, 2: b}}
	applyTree(t, sess, stale)
	if got := sessionTreeKey(sess, 1); got != "0/0.5 "+b {
		t.Fatalf("an op put a closed window back in the tree: %q", got)
	}
}

// TestLayoutTreeOpIsCountedWithItsChange: the op's number reaches PushSeen in
// the same snapshot that first holds the op's tree. A snapshot that said the
// op was seen and still held the old tree let its sender adopt the old tree
// over its own change.
func TestLayoutTreeOpIsCountedWithItsChange(t *testing.T) {
	sess, a, b := treeSession(t)
	var published []*SessionState
	sess.SetStateSink(func(st *SessionState) { published = append(published, st) })
	op := &LayoutTreePayload{PushOrigin: "A", PushSeq: 7, Workspace: 1,
		Tree: bspTree(bspSplit(1, 0.3, bspLeaf(1), bspLeaf(2))), Leaves: map[int]string{1: a, 2: b}}
	applyTree(t, sess, op)
	if len(published) != 1 {
		t.Fatalf("%d states published for one op, want 1", len(published))
	}
	st := published[0]
	if st.PushSeen["A"] != 7 {
		t.Fatalf("PushSeen[A] = %d in the op's state, want 7", st.PushSeen["A"])
	}
	if got, want := TreeKey(st.WorkspaceTrees[1], SessionTreeNames(st)), opKey(op); got != want {
		t.Fatalf("the op's state holds %q, want %q", got, want)
	}
}
