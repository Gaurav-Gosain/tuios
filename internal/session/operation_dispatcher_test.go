package session

import (
	"errors"
	"testing"
)

// TestDirectBSPTreeMutationPrevention verifies that when operation dispatch enforcement
// is active, direct mutation of the session-owned BSP tree via whole-state pushes
// (UpdateState) is guarded and reverted to canonical state, while zoom, float, ratios,
// and scroll strip state pushes continue to operate without interference.
func TestDirectBSPTreeMutationPrevention(t *testing.T) {
	cfg := &SessionConfig{Shell: "/bin/sh", EnforceOperationDispatch: true}
	sess, err := NewSession("guard-tree", cfg, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(sess.Stop)

	a, err := sess.AddDaemonWindow("a", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow a: %v", err)
	}
	b, err := sess.AddDaemonWindow("b", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow b: %v", err)
	}

	// Establish initial canonical BSP tree via operation dispatcher.
	initialTree := bspTree(bspSplit(1, 0.5, bspLeaf(1), bspLeaf(2)))
	initialLeaves := map[int]string{1: a.ID, 2: b.ID}
	if err := sess.SetLayoutTreeOp(1, initialTree, initialLeaves); err != nil {
		t.Fatalf("SetLayoutTreeOp: %v", err)
	}

	canonicalKey := sessionTreeKey(sess, 1)
	if canonicalKey == "" {
		t.Fatal("expected non-empty canonical tree key")
	}

	// 1. Attempt direct mutation of the BSP tree via UpdateState.
	tampered := clientSnapshot(sess)
	tampered.WorkspaceTrees = map[int]*SerializedBSPTree{
		1: bspTree(bspSplit(1, 0.9, bspLeaf(10), bspLeaf(20))),
	}
	tampered.WindowToBSPID = map[string]int{a.ID: 10, b.ID: 20}
	tampered.NextBSPWindowID = 21

	accepted := sess.UpdateState(tampered)
	if accepted {
		t.Errorf("UpdateState accepted tampered BSP tree under EnforceOperationDispatch; want false")
	}

	// Verify that the session retained its canonical BSP tree.
	if got := sessionTreeKey(sess, 1); got != canonicalKey {
		t.Errorf("session tree mutated to %q, want %q", got, canonicalKey)
	}

	// 2. Verify that non-BSP state pushes (zoom, float, ratios, scroll strip, sidebar)
	// are NOT blocked by the BSP tree guard.
	push := clientSnapshot(sess)
	push.WorkspaceTrees = nil // current clients omit trees from push
	win := windowByID(t, push, a.ID)
	win.Zoomed = true
	win.IsFloating = true
	push.WorkspaceMasterRatio = map[int]float64{1: 0.75}
	push.ScrollStrip = &ScrollStripState{ViewportX: 42}
	push.SidebarWidth = 30

	if !sess.UpdateState(push) {
		t.Fatalf("UpdateState rejected non-BSP state push with zoom/float/ratios/strip")
	}

	after := sess.GetState()
	afterWin := windowByID(t, after, a.ID)
	if afterWin == nil || !afterWin.Zoomed {
		t.Errorf("Zoomed state not preserved after push: %+v", afterWin)
	}
	if afterWin == nil || !afterWin.IsFloating {
		t.Errorf("IsFloating state not preserved after push: %+v", afterWin)
	}
	if got := after.WorkspaceMasterRatio[1]; got != 0.75 {
		t.Errorf("WorkspaceMasterRatio = %f, want 0.75", got)
	}
	if after.ScrollStrip == nil || after.ScrollStrip.ViewportX != 42 {
		t.Errorf("ScrollStrip = %v, want ViewportX=42", after.ScrollStrip)
	}
	if after.SidebarWidth != 30 {
		t.Errorf("SidebarWidth = %d, want 30", after.SidebarWidth)
	}
	// Verify tree still intact.
	if got := sessionTreeKey(sess, 1); got != canonicalKey {
		t.Errorf("canonical tree affected by non-BSP push: got %q, want %q", got, canonicalKey)
	}
}

// TestLayoutTreeOpDispatch verifies that LayoutTreeOp mutates the session's BSP tree
// under mutateState via the operation dispatcher and advances the session Version.
func TestLayoutTreeOpDispatch(t *testing.T) {
	sess, a, b := treeSession(t)
	v := sess.GetState().Version

	op := &LayoutTreeOp{
		PushOrigin: "client-1",
		Workspace:  1,
		Tree:       bspTree(bspSplit(1, 0.4, bspLeaf(5), bspLeaf(6))),
		Leaves:     map[int]string{5: a, 6: b},
	}

	if err := sess.DispatchOp(op); err != nil {
		t.Fatalf("DispatchOp(LayoutTreeOp): %v", err)
	}

	st := sess.GetState()
	if st.Version != v+1 {
		t.Fatalf("Version = %d, want %d", st.Version, v+1)
	}

	wantKey := TreeKey(op.Tree, func(n int) string { return op.Leaves[n] })
	if got := sessionTreeKey(sess, 1); got != wantKey {
		t.Fatalf("session tree = %q, want %q", got, wantKey)
	}

	// Dispatching an invalid workspace returns an error.
	badOp := &LayoutTreeOp{
		Workspace: 99,
		Tree:      bspTree(bspSplit(1, 0.5, bspLeaf(1), bspLeaf(2))),
		Leaves:    map[int]string{1: a, 2: b},
	}
	if err := sess.DispatchOp(badOp); err == nil {
		t.Errorf("DispatchOp with out-of-range workspace succeeded; want error")
	}

	// Nil operations or sessions return error.
	if err := sess.DispatchOp(nil); err == nil {
		t.Errorf("DispatchOp(nil) succeeded; want error")
	}
	d := NewOperationDispatcher()
	if err := d.Dispatch(nil, op); err == nil {
		t.Errorf("dispatcher.Dispatch(nil, op) succeeded; want error")
	}
}

// TestOperationDispatcherCustomHandler verifies handler registration and custom dispatch logic.
func TestOperationDispatcherCustomHandler(t *testing.T) {
	sess, a, b := treeSession(t)
	if !sess.Dispatcher().HasHandler("LayoutTree") {
		t.Errorf("expected default handler for LayoutTree")
	}

	called := false
	customErr := errors.New("custom intercepted error")
	sess.Dispatcher().Register("LayoutTree", func(state *SessionState, op SessionOp) error {
		called = true
		return customErr
	})

	op := &LayoutTreeOp{
		Workspace: 1,
		Tree:      bspTree(bspSplit(1, 0.5, bspLeaf(1), bspLeaf(2))),
		Leaves:    map[int]string{1: a, 2: b},
	}

	_, err := sess.ApplyLayoutTreeOp(op)
	if !called {
		t.Errorf("custom handler was not called")
	}
	if !errors.Is(err, customErr) {
		t.Errorf("err = %v, want %v", err, customErr)
	}
}

// TestLegacyClientTreeAllowedWhenEnforceDisabled verifies backward compatibility:
// when EnforceOperationDispatch is false (default), pushes with trees from legacy clients
// are accepted.
func TestLegacyClientTreeAllowedWhenEnforceDisabled(t *testing.T) {
	sess, a, b := treeSession(t)

	legacy := clientSnapshot(sess)
	legacy.WorkspaceTrees = map[int]*SerializedBSPTree{
		1: bspTree(bspSplit(2, 0.7, bspLeaf(30), bspLeaf(31))),
	}
	legacy.WindowToBSPID = map[string]int{a: 30, b: 31}
	legacy.NextBSPWindowID = 32

	if !sess.UpdateState(legacy) {
		t.Fatalf("UpdateState rejected legacy tree push with enforcement disabled")
	}

	want := TreeKey(legacy.WorkspaceTrees[1], SessionTreeNames(legacy))
	if got := sessionTreeKey(sess, 1); got != want {
		t.Fatalf("legacy tree not adopted: got %q, want %q", got, want)
	}
}
