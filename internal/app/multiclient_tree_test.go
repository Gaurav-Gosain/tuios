package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// A BSP tree is built by a client and needed by every client on the session:
// it is what the dividers between shared-border panes are read from, and it is
// what the next retile lays the panes out from. The daemon never builds one; a
// client sends it as an op and the daemon applies it (layout_tree_sync.go).
//
// This test holds two full clients on one session and watches what one
// client's tree does to the other's.
//
// NEGATIVE CONTROL: the unsentTree check cut from adoptSessionTrees, so a
// change this client has not sent yet no longer outranks a state from the
// daemon: TestAPeerKeepsItsOwnTreeAgainstADaemonEcho fails saying a
// same-version echo from the daemon replaced the stacked tree with the
// side-by-side one. Against a daemon too old for ops the adoptTopology gate
// in ApplyStateSyncFrom does the same job.

// treeShape renders a client's tree for the current workspace with the panes
// named by their PTYs rather than by int IDs, so two clients' trees can be
// compared whatever numbers each handed out.
func treeShape(m *OS) string {
	state := m.BuildSessionState()
	tree := state.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil || tree.Root == nil {
		return "<no tree>"
	}
	var walk func(n *session.SerializedBSPNode) string
	walk = func(n *session.SerializedBSPNode) string {
		if n == nil {
			return "_"
		}
		if n.Left == nil && n.Right == nil {
			if w := m.GetWindowByIntID(n.WindowID); w != nil {
				return shortID(w.PTYID)
			}
			return fmt.Sprintf("int%d", n.WindowID)
		}
		return fmt.Sprintf("split%d@%.2f(%s,%s)", n.SplitType, n.SplitRatio, walk(n.Left), walk(n.Right))
	}
	return walk(tree.Root)
}

// TestAPeerKeepsItsOwnTreeAgainstADaemonEcho pins the other half of the gate,
// which the fix above must not loosen: a state the daemon sends on its own
// account at a version this client already holds is an echo of this client's
// own push, and adopting it would undo a change made since.
func TestAPeerKeepsItsOwnTreeAgainstADaemonEcho(t *testing.T) {
	r, p, ex := geometryRig(t, clientGlobals{}, clientGlobals{})
	settleGeometry(t, r, p, ex)

	echo := r.m.BuildSessionState()
	echo.Version = r.m.DaemonStateVersion
	r.m.RotateFocusedSplit()
	want := treeShape(r.m)
	if err := r.m.ApplyStateSync(echo); err != nil {
		t.Fatal(err)
	}
	if got := treeShape(r.m); got != want {
		t.Errorf("a same-version echo from the daemon replaced the tree:\n before %s\n after  %s", want, got)
	}
}

// TestCycleTilingSchemeSyncsToAPeer: the scheme lives on the tree
// (layout.BSPTree.AutoScheme), which already travels with every layout-tree op
// (see session.SerializedBSPTree and session.TreeKey). Cycling it on one
// client is exactly that kind of tree change, so it reaches a peer the same
// way a split or a drag does, with no code of its own to send it.
func TestCycleTilingSchemeSyncsToAPeer(t *testing.T) {
	r, p, ex := geometryRig(t, clientGlobals{}, clientGlobals{})

	before := r.m.GetOrCreateBSPTree().AutoScheme
	scheme := r.m.CycleTilingScheme()
	if scheme == "" {
		t.Fatal("CycleTilingScheme did nothing while tiling was on")
	}
	after := r.m.GetOrCreateBSPTree().AutoScheme
	if after == before {
		t.Fatalf("CycleTilingScheme left the scheme at %v", before)
	}
	r.m.SyncStateToDaemon()
	settleTrees(t, r, p, ex, "after cycling the tiling scheme")

	peerTree := p.m.WorkspaceTrees[p.m.CurrentWorkspace]
	if peerTree == nil {
		t.Fatal("the peer has no tree for the workspace after settling")
	}
	if peerTree.AutoScheme != after {
		t.Fatalf("peer tree AutoScheme = %v, want %v (local cycled from %v)", peerTree.AutoScheme, after, before)
	}
}
