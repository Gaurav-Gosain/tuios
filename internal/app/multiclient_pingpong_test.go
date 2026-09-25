package app

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// A pane's PTY has exactly one size. Every client attached is looking at the
// same PTYs, so the box the panes are laid out in is not a per-client quantity,
// and any chrome a client draws around them has to come out of that client's
// own screen rather than out of the panes.
//
// These tests hold two full clients on one session (each with its own
// connection, its own OS and its own chrome) and watch what one client's
// ordinary push does to the other's panes and to the shared PTYs.

// peer is a second full client on the rig's session, restored and subscribed by
// the same route cmd/tuios uses.
type peer struct {
	m *OS
	c *session.TUIClient
}

// exchange routes every state broadcast either client receives into the other
// client's OS, which is what cmd/tuios does with StateSyncMsg, and counts them.
// A converging pair goes quiet; a pair that disagrees about the box does not.
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

// route wires one client's incoming broadcasts into one OS: the state pushes a
// peer makes, and the size the daemon settles on. Both are what the program
// loop does with them.
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
		e.enqueue(func() {
			e.n++
			m.Update(SessionResizeMsg{
				Width:       width,
				Height:      height,
				ClientCount: clientCount,
				Reserve:     reserve,
			})
		})
	})
}

// settle delivers queued broadcasts until none arrives for quiet, or until
// limit have been delivered, and reports how many were delivered.
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

// settleBox waits until the session has finished agreeing on the box and both
// clients have taken it, then forgets everything that happened on the way.
//
// Waiting for the exchange to go quiet is not enough, and the difference showed
// up as one failure in a hundred: the announcements above cross on the wire, so
// the daemon can settle the reserve twice, and the second broadcast can still be
// in flight when a fixed quiet period expires. It is then delivered by the next
// settle, inside the measurement, where applying it re-lays the panes out and
// resizes their shells, which is exactly what the test is counting.
//
// So the condition is the state rather than the silence: both clients hold the
// same session reserve, both have applied it, and the queue is empty. The
// clients' own copies are read rather than the daemon's, because they are what
// the OS lays out against and they are updated by the read loop the instant a
// broadcast lands, so a broadcast received but not yet applied cannot look
// settled.
func (e *exchange) settleBox(r *rig, p *peer) {
	e.t.Helper()
	deadline := time.Now().Add(rigWait)
	for {
		e.settle(200, 50*time.Millisecond)
		local, remote := r.client.SessionLayoutReserve(), p.c.SessionLayoutReserve()
		_, queued := e.take()
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

// joinPeerOS attaches a second OS client to the rig's session.
func joinPeerOS(t *testing.T, r *rig, cols, rows int) *peer {
	t.Helper()
	m, c := attachClientOS(t, r.session, cols, rows, false)
	return &peer{m: m, c: c}
}

// rects renders the tiled pane rectangles as text, so a failure names the
// layout rather than a boolean.
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

// contentSizes reports the grid each client believes every pane's guest is
// running at.
func contentSizes(m *OS) string {
	out := ""
	for _, w := range m.Windows {
		out += fmt.Sprintf("%s=%dx%d ", shortID(w.PTYID), w.ContentWidth(), w.ContentHeight())
	}
	return out
}

// twoClientsMidSizeChange is the transient no agreement can rule out: a session
// whose size has changed, where one client has heard and the other has not.
// There is a window of a few milliseconds on every resize where that is true,
// and for that window the two clients partition different boxes however well
// they agree about chrome.
//
// It is the state a sync loop starts from, and unlike a chrome disagreement it
// cannot be settled by sharing anything: the message that settles it is
// already on its way.
func twoClientsMidSizeChange(t *testing.T) (*rig, *peer, *exchange) {
	t.Helper()
	prevAnim := config.Global.AnimationsEnabled
	config.Global.AnimationsEnabled = false
	t.Cleanup(func() { config.Global.AnimationsEnabled = prevAnim })

	r := newRigSized(t, 2, holderCols, holderRows)
	r.tile()
	p := joinPeerOS(t, r, holderCols, holderRows)
	p.m.AutoTiling = true

	// The peer has taken a narrower session size that has not reached the local
	// client. Every rectangle the local client sends now overflows the peer's
	// box, and every rectangle the peer would send falls short of the local
	// client's.
	p.m.EffectiveWidth = holderCols - 24
	p.m.TileAllWindows()

	ex := &exchange{t: t}
	ex.route(r.client, r.m, "local")
	ex.route(p.c, p.m, "peer")
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

	// One push from the local client, into a peer that disagrees with every
	// rectangle in it. Nothing else here announces anything, so every delivery
	// the exchange counts from here on is a state push somebody chose to make.
	r.m.FocusedWindow = (r.m.FocusedWindow + 1) % len(r.m.Windows)
	r.m.SyncStateToDaemon()

	const limit = 60
	n := ex.settle(limit, 400*time.Millisecond)
	if n >= limit {
		t.Fatalf("the two clients were still trading state after %d rounds:\n local %s\n peer  %s",
			n, rects(r.m), rects(p.m))
	}
	// Exactly one delivery: the local client's push, into the peer. The peer
	// disagreed with every rectangle in it and said nothing back.
	if n != 1 {
		t.Fatalf("one push produced %d deliveries, so a client answered a sync with a layout:\n local %s\n peer  %s",
			n, rects(r.m), rects(p.m))
	}
	// And the disagreement is still a disagreement: the guard is what stops it
	// being announced, not something that quietly made the two boxes agree.
	if rects(r.m) == rects(p.m) {
		t.Fatalf("the two clients agreed on %s, so this test is no longer about a disagreement", rects(r.m))
	}
}
