package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// While a client too old for tree ops is attached, the daemon turns the ops
// off for the session (see Daemon.refreshTreeOps), and the trees travel in
// the pushes, numbered by whichever client pushed. These tests hand a current
// client such states directly.

// numberedAs is a copy of the local client's state with its trees renumbered
// so that each window gets the number the peer gives the other window. Taken
// raw, a tree in it puts the two panes in each other's places on the peer.
func numberedAs(t *testing.T, r *rig, p *peer, ops bool) *session.SessionState {
	t.Helper()
	st := r.m.BuildSessionState()
	if len(r.m.Windows) != 2 {
		t.Fatalf("setup: want two panes, have %d", len(r.m.Windows))
	}
	a, b := r.m.Windows[0].ID, r.m.Windows[1].ID
	swap := map[string]int{a: p.m.GetWindowIntID(b), b: p.m.GetWindowIntID(a)}
	local := map[int]int{r.m.GetWindowIntID(a): swap[a], r.m.GetWindowIntID(b): swap[b]}
	for ws, tree := range st.WorkspaceTrees {
		st.WorkspaceTrees[ws] = session.RemapTree(tree, func(n int) (int, bool) {
			m, ok := local[n]
			return m, ok
		})
	}
	st.WindowToBSPID = swap
	st.Version = p.m.DaemonStateVersion + 1
	st.BaseVersion = 0
	st.LayoutTreeOps = ops
	return st
}

// TestTreesWhileOpsAreOffAreReadByWindow: with the ops off, a tree arrives
// numbered by another client. The peer reads each leaf by window, so it shows
// the same layout as the client that pushed it.
//
// Negative control: with the gatedOff branch cut from ApplyStateSyncFrom, the
// peer takes the tree raw and shows the two panes swapped.
func TestTreesWhileOpsAreOffAreReadByWindow(t *testing.T) {
	r, p, _ := geometryRig(t, clientGlobals{}, clientGlobals{})
	r.m.ResizeFocusedWindowWidth(8) // an uneven split, so a swap shows
	want := treeShape(r.m)

	if err := p.m.ApplyStateSync(numberedAs(t, r, p, false)); err != nil {
		t.Fatal(err)
	}
	if p.m.treeOpsOn() {
		t.Fatal("the peer still sends tree ops after the session turned them off")
	}
	if got := treeShape(p.m); got != want {
		t.Fatalf("the peer read the tree by its numbers, not by window:\n want %s\n got  %s", want, got)
	}
}

// TestTreeOpsBackOnTakeTheSessionsTree: the peer changed its tree while the
// ops were off and pushed it the old way. When the ops come back on, the
// state that says so carries the session's tree, and the peer takes it; its
// own change is not an op still owed.
//
// Negative control: with the treeSeen reset cut from adoptTreeOpsFlag, the
// peer keeps its rotated tree as an unsent change.
func TestTreeOpsBackOnTakeTheSessionsTree(t *testing.T) {
	r, p, _ := geometryRig(t, clientGlobals{}, clientGlobals{})
	if err := p.m.ApplyStateSync(numberedAs(t, r, p, false)); err != nil {
		t.Fatal(err)
	}
	p.m.RotateFocusedSplit()
	if treeShape(p.m) == treeShape(r.m) {
		t.Fatal("setup: the rotate did not change the peer's tree")
	}

	on := numberedAs(t, r, p, true)
	if err := p.m.ApplyStateSync(on); err != nil {
		t.Fatal(err)
	}
	if !p.m.treeOpsOn() {
		t.Fatal("the peer does not send tree ops after the session turned them on")
	}
	if got, want := treeShape(p.m), treeShape(r.m); got != want {
		t.Fatalf("the peer kept its own tree when the ops came back on:\n want %s\n got  %s", want, got)
	}
	if unsent := p.m.unsentTrees(); len(unsent) != 0 {
		t.Fatalf("the peer holds trees it thinks it still owes the session: %v", unsent)
	}
}
