package app

import (
	"fmt"
	"testing"
	"time"
)

// Two clients reshaping one BSP tree at the same moment.
//
// The tree is sent to the daemon as an op (see layout_tree_sync.go and
// layout_tree.go in the session package). These tests hold two full clients on
// one session, make both of them change the tree before either has heard of the
// other's change, and then let every message through. The pair has to end on
// one tree, the tree a client attaching afterwards is handed has to be that
// same tree, and nothing may still be moving once it has settled.
//
// NEGATIVE CONTROL: measured on main, where the trees ride in the push and the
// later push wins. TestTwoClientsReshapeOneTreeAtOnce failed 6 runs in 10
// there, each time with the pair split for good, for example
//
//	local split2@0.48(d6ef0c95,3165730c)
//	peer  split1@0.51(d6ef0c95,3165730c)
//
// Each client's push reached the other, each dropped the other's as older
// than its own push, and neither ever sent again. The two tests after it pass
// on main too; they pin the new-pane race, which the op has to keep working.

// rootRatio is the ratio of the split at the top of the client's tree for the
// workspace on screen, or -1 when there is no split.
func rootRatio(m *OS) float64 {
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil || tree.Root == nil || tree.Root.IsLeaf() {
		return -1
	}
	return tree.Root.SplitRatio
}

// leafCount is how many panes the client's tree for the workspace on screen
// holds.
func leafCount(m *OS) int {
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		return 0
	}
	return len(tree.GetAllWindowIDs())
}

// settleTrees delivers broadcasts until both clients hold the same tree for
// the workspace on screen and nothing is queued, then checks the pair stays
// quiet. It reports the shape they settled on.
func settleTrees(t *testing.T, r *rig, p *peer, ex *exchange, what string) string {
	t.Helper()
	deadline := time.Now().Add(rigWait)
	for {
		ex.settle(400, 50*time.Millisecond)
		if !ex.queued() && treeShape(r.m) == treeShape(p.m) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: the pair never agreed on one tree:\n local %s\n peer  %s", what, treeShape(r.m), treeShape(p.m))
		}
		time.Sleep(10 * time.Millisecond)
	}
	shape := treeShape(r.m)
	// Settled means settled: a pair that agrees for a moment and then sends
	// each other another tree is the loop this whole design exists to rule
	// out.
	before := ex.n
	ex.settle(before+40, 300*time.Millisecond)
	if got := treeShape(r.m); got != shape || treeShape(p.m) != shape {
		t.Fatalf("%s: the pair moved again after settling:\n was   %s\n local %s\n peer  %s",
			what, shape, got, treeShape(p.m))
	}
	if n := ex.n - before; n > 2 {
		t.Fatalf("%s: %d more deliveries after the pair agreed on %s", what, n, shape)
	}
	// Each client shows the tree it holds. A pane off its tree is a client
	// drawing one layout while holding another, and it would also retile on
	// every state it is sent.
	if r.m.bspRectsOffTree() || p.m.bspRectsOffTree() {
		t.Fatalf("%s: a client's panes are not where its tree puts them:\n local %s %s\n peer  %s %s",
			what, shape, rects(r.m), treeShape(p.m), rects(p.m))
	}
	return shape
}

// attachedShape is the tree a client attaching now is handed, which is the
// session's own.
func attachedShape(t *testing.T, r *rig) string {
	t.Helper()
	m, _ := attachClientOS(t, r.session, holderCols, holderRows, false)
	return treeShape(m)
}

// TestTwoClientsReshapeOneTreeAtOnce: both clients change the tree inside one
// round trip of each other, many times over, with the order of the two ops at
// the daemon left to the scheduler. Every round has to end on one tree.
func TestTwoClientsReshapeOneTreeAtOnce(t *testing.T) {
	r, p, ex := geometryRig(t, clientGlobals{}, clientGlobals{})

	for round := range 30 {
		// Neither client has heard of the other's change when it makes its
		// own: both ops are built on the same tree.
		switch round % 3 {
		case 0:
			r.m.RotateFocusedSplit()
			p.m.ResizeFocusedWindowWidth(3)
		case 1:
			r.m.ResizeFocusedWindowWidth(-4)
			p.m.RotateFocusedSplit()
		case 2:
			r.m.ResizeFocusedWindowWidth(5)
			p.m.ResizeFocusedWindowWidth(-2)
		}
		r.m.SyncStateToDaemon()
		p.m.SyncStateToDaemon()
		settleTrees(t, r, p, ex, fmt.Sprintf("round %d", round))
	}
	final := treeShape(r.m)
	if got := attachedShape(t, r); got != final {
		t.Fatalf("a client attaching now is handed another tree:\n pair    %s\n attach  %s", final, got)
	}
}

// TestResizeWhilePeerAddsPaneKeepsTheResize: the peer asks for a new pane, the
// daemon makes it, and before this client has heard of the pane it resizes a
// split. The resize has to survive the peer placing the pane, and both
// clients have to end on one tree holding all three panes.
func TestResizeWhilePeerAddsPaneKeepsTheResize(t *testing.T) {
	r, p, ex := geometryRig(t, clientGlobals{}, clientGlobals{})
	before := rootRatio(r.m)

	p.m.AddWindow("")
	rigWaitUntil(t, "the daemon to hold the new pane", func() bool {
		list, err := r.ctl.RefreshSessionList()
		if err != nil {
			return false
		}
		for _, s := range list {
			if s.Name == r.session {
				return s.WindowCount == 3
			}
		}
		return false
	})

	// The pane is on the daemon and its broadcast is queued for both clients,
	// unread. This client resizes and says so first.
	r.m.ResizeFocusedWindowWidth(10)
	resized := rootRatio(r.m)
	if resized == before {
		t.Fatalf("the resize did not move the split (ratio %.3f)", resized)
	}
	r.m.SyncStateToDaemon()

	settleUntil(t, ex, "both clients to hold three panes in one tree", func() bool {
		return !ex.queued() && leafCount(r.m) == 3 && leafCount(p.m) == 3 &&
			treeShape(r.m) == treeShape(p.m)
	})
	shape := settleTrees(t, r, p, ex, "after the new pane")
	if got := rootRatio(r.m); got != resized {
		t.Fatalf("the resize was lost: split at %.3f, resized to %.3f, was %.3f\n tree %s",
			got, resized, before, shape)
	}
	if got := attachedShape(t, r); got != shape {
		t.Fatalf("a client attaching now is handed another tree:\n pair    %s\n attach  %s", shape, got)
	}
}

// TestResizeDuringPeerSplitConverges: the same race with the two changes the
// other way round in time. This client resizes over and over, the way a drag
// does, while the peer opens a pane; each resize step is sent before the answer
// to the previous one is back.
func TestResizeDuringPeerSplitConverges(t *testing.T) {
	r, p, ex := geometryRig(t, clientGlobals{}, clientGlobals{})

	p.m.AddWindow("")
	for step := range 8 {
		r.m.ResizeFocusedWindowWidth(1 + step%3)
		r.m.SyncStateToDaemon()
	}
	settleUntil(t, ex, "both clients to hold three panes in one tree", func() bool {
		return !ex.queued() && leafCount(r.m) == 3 && leafCount(p.m) == 3 &&
			treeShape(r.m) == treeShape(p.m)
	})
	shape := settleTrees(t, r, p, ex, "after the drag and the new pane")
	if got := attachedShape(t, r); got != shape {
		t.Fatalf("a client attaching now is handed another tree:\n pair    %s\n attach  %s", shape, got)
	}
}
