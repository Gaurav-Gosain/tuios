package app

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A user action that crosses a peer's tree op on the wire.
//
// These came from the review of the tree op. Each one lets a peer's tree op
// land first and then makes a change on this client that was built before the
// client heard of the op. The change has to survive.
//
// NEGATIVE CONTROLS, measured on the first version of the op:
//   - TestMinimizeWhilePeerResizesStands and TestMoveWhilePeerResizesStands
//     failed. A peer's tree op counted as a mutation the push had missed, and
//     the reconcile took Minimized and Workspace back from the daemon.
//   - TestDragAfterPeerOpStands failed with the user at 0.371 and the screen
//     at 0.554. The daemon refused every step of the drag as built on an older
//     tree, because each step carried the same old base.

// TestDragAfterPeerOpStands: the peer's op lands, and then this client drags
// eight steps, each built before it heard of the peer's op. The drag stands.
func TestDragAfterPeerOpStands(t *testing.T) {
	r, p, ex := geometryRig(t, clientGlobals{}, clientGlobals{})
	p.m.ResizeFocusedWindowWidth(-6)
	p.m.SyncStateToDaemon()
	time.Sleep(150 * time.Millisecond) // the op lands, its broadcast queued and unread
	for range 8 {
		r.m.ResizeFocusedWindowWidth(2)
		r.m.SyncStateToDaemon()
	}
	dragged := rootRatio(r.m)
	settleTrees(t, r, p, ex, "after the drag")
	if got := rootRatio(r.m); got != dragged {
		t.Fatalf("the drag was lost: dragged to %.3f, the screen shows %.3f", dragged, got)
	}
}

// TestSlowDragCrossingPeerOpConverges: the peer's op crosses the first step of
// a drag, and each later step waits for the answer to the one before. The pair
// ends on one tree.
func TestSlowDragCrossingPeerOpConverges(t *testing.T) {
	r, p, ex := geometryRig(t, clientGlobals{}, clientGlobals{})
	p.m.ResizeFocusedWindowWidth(-6)
	r.m.ResizeFocusedWindowWidth(2)
	p.m.SyncStateToDaemon()
	time.Sleep(100 * time.Millisecond)
	r.m.SyncStateToDaemon()
	for range 6 {
		ex.settle(ex.n+50, 80*time.Millisecond)
		r.m.ResizeFocusedWindowWidth(2)
		r.m.SyncStateToDaemon()
	}
	dragged := rootRatio(r.m)
	settleTrees(t, r, p, ex, "after the slow drag")
	if got := rootRatio(r.m); got != dragged {
		t.Fatalf("the last step of the drag was lost: dragged to %.3f, the screen shows %.3f", dragged, got)
	}
}

// leavesByWorkspace names the leaves of every tree a client holds, each with
// the workspace its window is on.
func leavesByWorkspace(m *OS) map[int][]string {
	out := map[int][]string{}
	for ws, tree := range m.WorkspaceTrees {
		if tree == nil {
			continue
		}
		for _, n := range tree.GetAllWindowIDs() {
			id := "?"
			if w := m.GetWindowByIntID(n); w != nil {
				id = shortID(w.ID) + "@ws" + fmt.Sprint(w.Workspace)
			}
			out[ws] = append(out[ws], id)
		}
	}
	return out
}

// settleCrossing lets every message through and waits a little longer, so a
// late reconcile has time to arrive and undo something.
func settleCrossing(ex *exchange) {
	ex.settle(400, 300*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	ex.settle(400, 300*time.Millisecond)
}

// TestMoveWhilePeerResizesStands: this client moves a pane to workspace 2
// after the peer's op on workspace 1 has landed. The move stands, and no tree
// on any client holds a pane from another workspace.
func TestMoveWhilePeerResizesStands(t *testing.T) { checkMove(t, true) }

// TestMoveStands is the same move with no peer op, the baseline.
func TestMoveStands(t *testing.T) { checkMove(t, false) }

func checkMove(t *testing.T, peerOp bool) {
	r, p, ex := geometryRig(t, clientGlobals{}, clientGlobals{})
	if peerOp {
		p.m.ResizeFocusedWindowWidth(4)
		p.m.SyncStateToDaemon()
		time.Sleep(150 * time.Millisecond)
	}
	idx := 1 - r.m.FocusedWindow
	id := r.m.Windows[idx].ID
	r.m.MoveWindowToWorkspace(idx, 2)
	r.m.SyncStateToDaemon()
	settleCrossing(ex)
	joined, _ := attachClientOS(t, r.session, holderCols, holderRows, false)
	for label, m := range map[string]*OS{"local": r.m, "peer": p.m, "attached": joined} {
		for _, w := range m.Windows {
			if w.ID == id && w.Workspace != 2 {
				t.Errorf("%s: the move was lost, %s is on workspace %d", label, shortID(id), w.Workspace)
			}
		}
		for ws, leaves := range leavesByWorkspace(m) {
			for _, l := range leaves {
				if !strings.HasSuffix(l, fmt.Sprintf("@ws%d", ws)) {
					t.Errorf("%s: leaf %s is in the tree of workspace %d", label, l, ws)
				}
			}
		}
	}
}

// TestMinimizeWhilePeerResizesStands: this client minimises a pane after the
// peer's op has landed. The minimise stands on both clients.
func TestMinimizeWhilePeerResizesStands(t *testing.T) {
	r, p, ex := geometryRig(t, clientGlobals{}, clientGlobals{})
	p.m.ResizeFocusedWindowWidth(4)
	p.m.SyncStateToDaemon()
	time.Sleep(150 * time.Millisecond)
	idx := 1 - r.m.FocusedWindow
	id := r.m.Windows[idx].ID
	r.m.MinimizeWindow(idx)
	r.m.SyncStateToDaemon()
	settleCrossing(ex)
	for label, m := range map[string]*OS{"local": r.m, "peer": p.m} {
		for _, w := range m.Windows {
			if w.ID == id && !w.Minimized {
				t.Errorf("%s: the minimise was lost, %s is not minimised", label, shortID(id))
			}
		}
	}
}

// TestFocusWhilePeerResizesStands: this client moves the focus after the
// peer's op has landed. The focus stands.
func TestFocusWhilePeerResizesStands(t *testing.T) {
	r, p, ex := geometryRig(t, clientGlobals{}, clientGlobals{})
	p.m.ResizeFocusedWindowWidth(4)
	p.m.SyncStateToDaemon()
	time.Sleep(150 * time.Millisecond)
	idx := 1 - r.m.FocusedWindow
	id := r.m.Windows[idx].ID
	r.m.FocusWindow(idx)
	r.m.SyncStateToDaemon()
	settleCrossing(ex)
	if got := r.m.Windows[r.m.FocusedWindow].ID; got != id {
		t.Errorf("the focus was lost: want %s, got %s", shortID(id), shortID(got))
	}
}
