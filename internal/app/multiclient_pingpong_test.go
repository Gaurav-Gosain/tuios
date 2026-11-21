package app

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

type peer struct {
	m *OS
	c *session.TUIClient
}

type exchange struct {
	t  *testing.T
	mu sync.Mutex
	q  []func()
	n  int
}

func (e *exchange) enqueue(f func()) {
	e.mu.Lock()
	e.q = append(e.q, f)
	e.mu.Unlock()
}

func (e *exchange) take() (func(), bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.q) == 0 {
		return nil, false
	}
	f := e.q[0]
	e.q = e.q[1:]
	return f, true
}

func (e *exchange) queued() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.q) > 0
}

func (e *exchange) route(c *session.TUIClient, m *OS, label string) {
	c.OnStateSync(func(state *session.SessionState, _, sourceID string) {
		e.enqueue(func() {
			e.n++
			if err := m.ApplyStateSyncFrom(state, sourceID); err != nil {
				e.t.Errorf("%s: apply state sync: %v", label, err)
			}
		})
	})
	c.OnSessionResize(func(width, height, clientCount int, reserve session.LayoutReserve) {
		gen := c.SessionLayoutGeneration()
		e.enqueue(func() {
			e.n++
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

func (e *exchange) settle(limit int, quiet time.Duration) int {
	for e.n < limit {
		f, ok := e.take()
		if !ok {
			deadline := time.Now().Add(quiet)
			for time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
				if f, ok = e.take(); ok {
					break
				}
			}
			if !ok {
				return e.n
			}
		}
		f()
	}
	return e.n
}

func (e *exchange) settleBox(r *rig, p *peer) {
	e.t.Helper()
	deadline := time.Now().Add(rigWait)
	for {
		e.settle(200, 50*time.Millisecond)
		local, remote := r.client.SessionLayoutReserve(), p.c.SessionLayoutReserve()
		queued := e.queued()
		if queued == false && local == remote &&
			r.m.SessionReserve == local && p.m.SessionReserve == remote {
			e.n = 0
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("the session never settled on one box:\n local client %+v os %+v\n peer  client %+v os %+v",
				local, r.m.SessionReserve, remote, p.m.SessionReserve)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func joinPeerOS(t *testing.T, r *rig, cols, rows int) *peer {
	t.Helper()
	m, c := attachClientOS(t, r.session, cols, rows, false)
	return &peer{m: m, c: c}
}

func rects(m *OS) string {
	out := ""
	for _, w := range m.Windows {
		if w.Workspace != m.CurrentWorkspace || w.Minimized || w.IsFloating {
			continue
		}
		out += fmt.Sprintf("%s@%d,%d %dx%d ", shortID(w.PTYID), w.X, w.Y, w.Width, w.Height)
	}
	return out
}

func contentSizes(m *OS) string {
	out := ""
	for _, w := range m.Windows {
		out += fmt.Sprintf("%s=%dx%d ", shortID(w.PTYID), w.ContentWidth(), w.ContentHeight())
	}
	return out
}

func twoClientsMidSizeChange(t *testing.T) (*rig, *peer, *exchange) {
	t.Helper()
	prevAnim := config.Global.Motion
	config.Global.Motion = config.MotionNone
	t.Cleanup(func() { config.Global.Motion = prevAnim })

	r := newRigSized(t, 2, holderCols, holderRows)
	r.tile()
	ex := &exchange{t: t}
	r.m.SyncStateToDaemon()
	ex.route(r.client, r.m, "local")
	ex.settle(10, 200*time.Millisecond)
	p := joinPeerOS(t, r, holderCols, holderRows)
	p.m.AutoTiling = true

	p.m.EffectiveWidth = holderCols - 24
	p.m.TileAllWindows()

	ex.route(p.c, p.m, "peer")
	ex.n = 0
	return r, p, ex
}

// TestApplyingAPeerSyncPushesNothingBack is the guard that makes a sync loop
// impossible rather than unlikely.
//
// Two clients partitioning different boxes will always work out different
// rectangles for the same panes. That is a disagreement, and a disagreement
// settles. What does not settle is a disagreement each side announces: A pushes
// its layout, B reads rectangles that do not fit its box and pushes its own, A
// reads those and pushes again, and neither ever runs out of things to say. The
// rectangles are the argument and every round trip is a resize of a real shell.
//
// So a client may not push a layout it worked out because it disagreed with a
// peer's. It may push one thing from inside a sync (a window the daemon asked
// it to place, which is an answer rather than an echo), and that answer is
// terminal: the peer applying it has nothing left to place and so has nothing
// to say back.
//
// NEGATIVE CONTROL: measured, not assumed. Removing the applyingPeerSync guard
// in SyncStateToDaemon and letting ApplyStateSync's re-layout push (the shape
// of fix this area invites) turns this into an exchange that does
// not stop. Run that way it reaches the sixty-round cap with the two clients
// still trading rectangles, 60-0 wide against 48-48, and would have gone on for
// as long as the test let it.
func TestApplyingAPeerSyncPushesNothingBack(t *testing.T) {
	r, p, ex := twoClientsMidSizeChange(t)

	r.m.FocusedWindow = (r.m.FocusedWindow + 1) % len(r.m.Windows)
	r.m.SyncStateToDaemon()

	const limit = 60
	n := ex.settle(limit, 400*time.Millisecond)
	if n >= limit {
		t.Fatalf("the two clients were still trading state after %d rounds:\n local %s\n peer  %s",
			n, rects(r.m), rects(p.m))
	}
	if n != 1 {
		t.Fatalf("one push produced %d deliveries, so a client answered a sync with a layout:\n local %s\n peer  %s",
			n, rects(r.m), rects(p.m))
	}
	if rects(r.m) == rects(p.m) {
		t.Fatalf("the two clients agreed on %s, so this test is no longer about a disagreement", rects(r.m))
	}
}
