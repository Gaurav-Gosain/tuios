package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// A BSP tree is built by a client and needed by every client on the session:
// it is what the dividers between shared-border panes are read from, and it is
// what the next retile lays the panes out from. The daemon never builds one; it
// stores whichever a client last pushed and hands it on.
//
// These tests hold two full clients on one session and watch what one client's
// tree does to the other's. The report they exist for: a peer that watched
// tiling turn on drew a box around every borderless pane, with a blank column
// where the divider should be, and its first retile then built a tree of its
// own that disagreed with the one it had been sent.
//
// NEGATIVE CONTROLS, each run by mutating the shipped code and watching the
// named assertion fail:
//
//   - ApplyStateSyncFrom adopting the tree from a strictly newer state only
//     (adoptTopology := newerState), which is the tree before this fix:
//     TestAPeerAdoptsATreeAnotherClientBuilt fails in settleGeometry, the
//     pair never agreeing on one arithmetic because the peer's panes keep a
//     border of their own (118x17 against 120x19) after tiling turned on -
//     which is the frame in the report, a box around every borderless pane.
//   - The exchange applying every sync as the daemon's own
//     (ApplyStateSync(state) in place of ApplyStateSyncFrom): the peer holds
//     a side-by-side tree while the client that built it holds a stacked one,
//     which is what binds the test to the origin reaching the client rather
//     than to the gate alone.
//   - adoptTopology answering true for every sync, so the echo gate is gone:
//     TestAPeerKeepsItsOwnTreeAgainstADaemonEcho fails saying a same-version
//     echo from the daemon replaced the stacked tree with the side-by-side one.

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

// borders reports which of a client's tiled panes still draw a border of their
// own, which under shared borders should be none of them.
func borders(m *OS) string {
	var out []string
	for _, w := range m.Windows {
		if w.Workspace != m.CurrentWorkspace || w.Minimized || w.IsFloating {
			continue
		}
		if !w.Tiled {
			out = append(out, shortID(w.PTYID))
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, " ")
}

// treeRig is geometryRig with the peer joining while tiling is off, which is
// the report's shape: a client that attaches to an untiled session is given no
// tree, so it has nothing to fall back on when tiling turns on.
func treeRig(t *testing.T) (*rig, *peer, *exchange) {
	t.Helper()
	g := clientGlobals{shared: true}
	prevShared, prevGap := config.Global.SharedBorders, config.Global.PaneGap
	prevAnim := config.Global.AnimationsEnabled
	config.Global.AnimationsEnabled = false
	t.Cleanup(func() {
		config.Global.SharedBorders, config.Global.PaneGap = prevShared, prevGap
		config.Global.AnimationsEnabled = prevAnim
	})
	g.install()

	r := newRigSized(t, 2, holderCols, holderRows)
	r.m.AutoTiling = false
	r.m.SyncStateToDaemon()
	p := joinPeerOS(t, r, holderCols, holderRows)
	if p.m.AutoTiling || p.m.WorkspaceTrees[p.m.CurrentWorkspace] != nil {
		t.Fatalf("the peer joined tiled: tiling=%t tree=%s", p.m.AutoTiling, treeShape(p.m))
	}

	ex := &exchange{t: t}
	routeSide(ex, r.client, r.m, "local", g)
	routeSide(ex, p.c, p.m, "peer", g)
	r.m.AnnounceLayoutReserve()
	p.m.AnnounceLayoutReserve()
	ex.settleBox(r, p)
	ex.n = 0
	return r, p, ex
}

// TestAPeerAdoptsATreeAnotherClientBuilt is the report. Two clients with
// shared borders, the second attached while tiling was off, and tiling turned
// on from the first, which reshapes the tree before the other hears about it.
// The peer has to end up borderless, holding the same tree, and keep holding
// it through a retile of its own.
func TestAPeerAdoptsATreeAnotherClientBuilt(t *testing.T) {
	r, p, ex := treeRig(t)

	// Tiling on, and the split rotated, so the tree the local client pushes is
	// not the one a fresh spiral would build. Both are one push.
	r.m.SetAutoTiling(true)
	r.m.RotateFocusedSplit()
	r.m.SyncStateToDaemon()
	want := treeShape(r.m)
	if strings.HasPrefix(want, "<") {
		t.Fatalf("the local client has no tree to push: %s", want)
	}
	settleUntil(t, ex, "the peer to see tiling turn on", func() bool { return p.m.AutoTiling })
	settleGeometry(t, r, p, ex)

	// The frame symptom: with shared borders on, every tiled pane gives up its
	// own border. A peer with no tree cannot place dividers and refuses to.
	if got := borders(p.m); got != "none" {
		t.Errorf("the peer's panes still draw their own borders: %s\n peer tree %s", got, treeShape(p.m))
	}
	if got := treeShape(p.m); got != want {
		t.Errorf("the peer holds a different tree from the client that built it:\n local %s\n peer  %s", want, got)
	}
	if local, peer := rects(r.m), rects(p.m); local != peer {
		t.Errorf("clients disagree on pane rectangles:\n local %s\n peer  %s", local, peer)
	}

	// The second half of the report: the peer's own retile used to build a
	// fresh spiral for the rectangles it had, and the two clients then pushed
	// rectangles at each other.
	p.m.TileAllWindows()
	if got := treeShape(p.m); got != want {
		t.Errorf("the peer's retile replaced the adopted tree:\n local %s\n peer  %s", want, got)
	}
	p.m.SyncStateToDaemon()
	settleGeometry(t, r, p, ex)
	if got := treeShape(r.m); got != want {
		t.Errorf("the peer's push changed the tree on the client that built it:\n before %s\n after  %s", want, got)
	}
	if local, peer := rects(r.m), rects(p.m); local != peer {
		t.Errorf("clients disagree on pane rectangles after the peer's retile:\n local %s\n peer  %s", local, peer)
	}
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
