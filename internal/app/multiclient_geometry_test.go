package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

type clientGlobals struct {
	shared bool
	gap    int
}

func (g clientGlobals) install() {
	config.Global.SharedBorders = g.shared
	config.Global.PaneGap = g.gap
}

func routeSide(ex *exchange, c *session.TUIClient, m *OS, label string, g clientGlobals) {
	c.OnStateSync(func(state *session.SessionState, _, sourceID string) {
		ex.enqueue(func() {
			ex.n++
			g.install()
			if err := m.ApplyStateSyncFrom(state, sourceID); err != nil {
				ex.t.Errorf("%s: apply state sync: %v", label, err)
			}
		})
	})
	c.OnSessionResize(func(width, height, clientCount int, reserve session.LayoutReserve) {
		gen := c.SessionLayoutGeneration()
		ex.enqueue(func() {
			ex.n++
			g.install()
			m.Update(SessionResizeMsg{
				Width:       width,
				Height:      height,
				ClientCount: clientCount,
				Reserve:     reserve,
				Generation:  gen,
			})
		})
	})
}

func geometryRig(t *testing.T, localG, peerG clientGlobals) (*rig, *peer, *exchange) {
	t.Helper()
	prevShared, prevGap := config.Global.SharedBorders, config.Global.PaneGap
	prevAnim := config.Global.Motion
	config.Global.Motion = config.MotionNone
	t.Cleanup(func() {
		config.Global.SharedBorders, config.Global.PaneGap = prevShared, prevGap
		config.Global.Motion = prevAnim
	})

	localG.install()
	r := newRigSized(t, 2, holderCols, holderRows)
	r.tile()
	r.m.SyncStateToDaemon()

	peerG.install()
	p := joinPeerOS(t, r, holderCols, holderRows)
	p.m.AutoTiling = true

	ex := &exchange{t: t}
	routeSide(ex, r.client, r.m, "local", localG)
	routeSide(ex, p.c, p.m, "peer", peerG)

	localG.install()
	r.m.AnnounceLayoutReserve()
	peerG.install()
	p.m.AnnounceLayoutReserve()
	ex.settleBox(r, p)

	peerG.install()
	p.m.TileAllWindows()
	p.m.SyncDaemonPTYDimensions()
	settleGeometry(t, r, p, ex)
	settleUntil(t, ex, "both clients to hold one tree", func() bool {
		return !ex.queued() && treeShape(r.m) != "<no tree>" && treeShape(r.m) == treeShape(p.m)
	})
	ex.n = 0
	return r, p, ex
}

func settleGeometry(t *testing.T, r *rig, p *peer, ex *exchange) {
	t.Helper()
	deadline := time.Now().Add(rigWait)
	for {
		ex.settle(400, 50*time.Millisecond)
		queued := ex.queued()
		if !queued &&
			r.m.SharedBorders == p.m.SharedBorders && r.m.PaneGap == p.m.PaneGap &&
			contentSizes(r.m) == contentSizes(p.m) {
			return
		}
		if time.Now().After(deadline) {
			_, daemon := r.paneSizes()
			t.Fatalf("the pair never settled on one arithmetic:\n local shared=%v gap=%d %s\n peer  shared=%v gap=%d %s\n"+
				" local tree %s rects %s tiling=%v ws=%d box=%dx%d reserve=%+v\n"+
				" peer  tree %s rects %s tiling=%v ws=%d box=%dx%d reserve=%+v\n daemon %v",
				r.m.SharedBorders, r.m.PaneGap, contentSizes(r.m),
				p.m.SharedBorders, p.m.PaneGap, contentSizes(p.m),
				treeShape(r.m), rects(r.m), r.m.AutoTiling, r.m.CurrentWorkspace, r.m.GetRenderWidth(), r.m.GetRenderHeight(), r.m.SessionReserve,
				treeShape(p.m), rects(p.m), p.m.AutoTiling, p.m.CurrentWorkspace, p.m.GetRenderWidth(), p.m.GetRenderHeight(), p.m.SessionReserve,
				daemon)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func settleUntil(t *testing.T, ex *exchange, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(rigWait)
	for {
		ex.settle(400, 50*time.Millisecond)
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestFloatedPaneStaysFloatedEverywhere pins the float flag as session state.
// A float is layout intent: a peer that does not know a pane floats counts it
// among the tiled panes, tiles it back into the box, and pushes the result,
// which destroys the float and moves every shared PTY.
//
// NEGATIVE CONTROL: measured on the unfixed tree. The peer's copy of the
// floated pane keeps IsFloating=false and the peer's layout still tiles it.
func TestFloatedPaneStaysFloatedEverywhere(t *testing.T) {
	r, p, ex := geometryRig(t, clientGlobals{}, clientGlobals{})

	r.m.FocusedWindow = 0
	r.m.ToggleFloating()
	floatedID := r.m.Windows[0].ID
	r.m.SyncStateToDaemon()
	peerCopy := func() bool {
		for _, w := range p.m.Windows {
			if w.ID == floatedID {
				return w.IsFloating
			}
		}
		return false
	}
	settleUntil(t, ex, "the peer to learn pane "+shortID(floatedID)+" floats", peerCopy)
	settleGeometry(t, r, p, ex)
	if tree := p.m.WorkspaceTrees[p.m.CurrentWorkspace]; tree != nil {
		intID := p.m.GetWindowIntID(floatedID)
		for _, id := range tree.GetAllWindowIDs() {
			if id == intID {
				t.Fatalf("the peer's tree still holds the floated pane")
			}
		}
	}
	if local, peer := contentSizes(r.m), contentSizes(p.m); local != peer {
		t.Fatalf("clients disagree on pane sizes after the float:\n local %s\n peer  %s", local, peer)
	}
}
