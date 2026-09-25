package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// A pane's box is settled across a session's clients (the negotiated reserve),
// but the arithmetic inside the box used to read process-global config that
// nothing synced: shared borders and the pane gap. Two clients whose configs
// disagreed there partitioned the same box into different rectangles, or the
// same rectangles into different guest grids, and every ordinary state push
// (a focus switch, alt+n) moved the shared PTYs between the two answers.
//
// These tests hold two full clients on one session whose *processes* disagree
// about the geometry config, which is what a local client and a tuios-web
// process with different config in force are. Process disagreement is
// simulated by installing each side's globals before anything runs on that
// side; after the fix the layout reads session-settled model state, so the
// globals only decide what each client walks in with.

// clientGlobals is one simulated client process's geometry config.
type clientGlobals struct {
	shared bool
	gap    int
}

func (g clientGlobals) install() {
	config.Global.SharedBorders = g.shared
	config.Global.PaneGap = g.gap
}

// routeSide is exchange.route with one side's globals installed before each of
// that side's closures runs, simulating a separate process. It doubles as a
// canary: if any layout path still reads the globals, the disagreeing tests
// below see the resizes come back.
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
		ex.enqueue(func() {
			ex.n++
			g.install()
			m.Update(SessionResizeMsg{
				Width:       width,
				Height:      height,
				ClientCount: clientCount,
				Reserve:     reserve,
			})
		})
	})
}

// geometryRig brings up a tiled two-pane session under localG's process, joins
// a peer under peerG's process, and settles the session: reserves agreed, the
// local client's state (which carries the session's pane geometry) pushed and
// adopted, and the exchange quiet.
func geometryRig(t *testing.T, localG, peerG clientGlobals) (*rig, *peer, *exchange) {
	t.Helper()
	prevShared, prevGap := config.Global.SharedBorders, config.Global.PaneGap
	prevAnim := config.Global.AnimationsEnabled
	config.Global.AnimationsEnabled = false
	t.Cleanup(func() {
		config.Global.SharedBorders, config.Global.PaneGap = prevShared, prevGap
		config.Global.AnimationsEnabled = prevAnim
	})

	localG.install()
	r := newRigSized(t, 2, holderCols, holderRows)
	r.tile()
	// The session's pane geometry is on the daemon before the peer attaches,
	// which is the usual case: the session existed before the second client.
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
	ex.n = 0
	return r, p, ex
}

// settleGeometry waits for the pair to genuinely converge (one agreed
// arithmetic, one set of pane sizes, an empty queue) rather than for a fixed
// quiet window. The lesson is settleBox's: on a loaded machine a broadcast can
// outlive any constant, and one delivered after a fixed window expires lands
// inside the measurement, where the settling it performs is exactly what the
// test is counting.
func settleGeometry(t *testing.T, r *rig, p *peer, ex *exchange) {
	t.Helper()
	deadline := time.Now().Add(rigWait)
	for {
		ex.settle(400, 50*time.Millisecond)
		_, queued := ex.take()
		if !queued &&
			r.m.SharedBorders == p.m.SharedBorders && r.m.PaneGap == p.m.PaneGap &&
			contentSizes(r.m) == contentSizes(p.m) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pair never settled on one arithmetic:\n local shared=%v gap=%d %s\n peer  shared=%v gap=%d %s",
				r.m.SharedBorders, r.m.PaneGap, contentSizes(r.m),
				p.m.SharedBorders, p.m.PaneGap, contentSizes(p.m))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// settleUntil delivers broadcasts until cond holds, so an assertion can wait
// for the event it needs rather than for a fixed window a loaded machine can
// outlast, and so a message that never arrives is a named failure rather
// than a vacuous pass.
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
	// The peer's tree must have let go of the pane, or its tiled panes underfill
	// the box forever and every sync reads as a stale layout.
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
