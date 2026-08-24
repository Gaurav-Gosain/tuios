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
// These tests hold two full clients on one session - each with its own
// connection, its own OS and its own chrome - and watch what one client's
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
	c.OnStateSync(func(state *session.SessionState, _, _ string) {
		e.enqueue(func() {
			e.n++
			if err := m.ApplyStateSync(state); err != nil {
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

// joinPeerOS attaches a second OS client to the rig's session.
func joinPeerOS(t *testing.T, r *rig, cols, rows int) *peer {
	t.Helper()
	m, c := attachClientOS(t, r.session, cols, rows, false)
	return &peer{m: m, c: c}
}

// resizeLog records every size a client announces to a pane's PTY, so a test
// can say how many SIGWINCHes an action cost and what sizes they carried.
type resizeLog struct {
	mu   sync.Mutex
	sent []string
}

func (l *resizeLog) watch(m *OS, label string) {
	for _, w := range m.Windows {
		w := w
		inner := w.DaemonResizeFunc
		if inner == nil {
			continue
		}
		w.DaemonResizeFunc = func(width, height int) error {
			l.mu.Lock()
			l.sent = append(l.sent, fmt.Sprintf("%s->%s %dx%d", label, shortID(w.PTYID), width, height))
			l.mu.Unlock()
			return inner(width, height)
		}
	}
}

func (l *resizeLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.sent...)
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

// twoClientsDisagreeingOnChrome brings up a tiled session with two clients the
// same negotiated size whose only difference is their own chrome: the local
// client has its rail folded and the peer has it open, and neither has told
// anyone. That is where the report starts, because the rail is the one piece of
// chrome a person changes without thinking of it as a change to the layout.
//
// Both mechanisms bear on it and they are meant to. The rail is session state
// now, so the two agree once a sync has passed; the reserve is negotiated, so
// they would have laid the panes out in the same box even if it were not.
func twoClientsDisagreeingOnChrome(t *testing.T) (*rig, *peer, *exchange) {
	t.Helper()
	prevAnim := config.AnimationsEnabled
	prevEnabled := config.SidebarEnabled
	prevWidth := config.SidebarWidth
	prevPos := config.SidebarPosition
	config.AnimationsEnabled = false
	config.SidebarEnabled = true
	config.SidebarWidth = 24
	config.SidebarPosition = "left"
	t.Cleanup(func() {
		config.AnimationsEnabled = prevAnim
		config.SidebarEnabled = prevEnabled
		config.SidebarWidth = prevWidth
		config.SidebarPosition = prevPos
	})

	r := newRigSized(t, 2, holderCols, holderRows)
	r.m.SidebarCollapsed = true
	r.tile()

	p := joinPeerOS(t, r, holderCols, holderRows)
	p.m.AutoTiling = true

	ex := &exchange{t: t}
	ex.route(r.client, r.m, "local")
	ex.route(p.c, p.m, "peer")

	// Each client says what it keeps for its own chrome, which is what the
	// first window-size message does in cmd/tuios, and the session settles on
	// one box before anything else happens.
	r.m.AnnounceLayoutReserve()
	p.m.AnnounceLayoutReserve()
	ex.settle(40, 400*time.Millisecond)
	ex.n = 0

	p.m.TileAllWindows()
	p.m.SyncDaemonPTYDimensions()
	return r, p, ex
}

// TestTwoClientsAgreeOnEveryPaneSize is the invariant the report violates: a
// PTY has one size, so both clients and the daemon have to name the same one.
//
// NEGATIVE CONTROL: measured. On the unfixed tree - no agreed reserve and a
// rail nothing shares - it fails with the two clients running the same two
// shells at 56x36 and 57x36 on one side and 46x36 on the other. It is the
// invariant rather than either mechanism, so it is satisfied by either one on
// its own: with the rail shared but the reserve still private it passes,
// because then the two clients have the same chrome to fold in. What it would
// catch is any route back to two clients disagreeing about a shell's size.
func TestTwoClientsAgreeOnEveryPaneSize(t *testing.T) {
	r, p, ex := twoClientsDisagreeingOnChrome(t)

	// An ordinary pane switch on the local client: the one thing the report
	// says is enough to set it off.
	r.m.FocusedWindow = (r.m.FocusedWindow + 1) % len(r.m.Windows)
	r.m.SyncStateToDaemon()
	ex.settle(40, 400*time.Millisecond)

	local, peerSizes := contentSizes(r.m), contentSizes(p.m)
	t.Logf("local rects: %s", rects(r.m))
	t.Logf("peer  rects: %s", rects(p.m))
	if local != peerSizes {
		t.Fatalf("the two clients run the same PTYs at different sizes:\n local %s\n peer  %s", local, peerSizes)
	}
	for _, w := range r.m.Windows {
		dw, dh := r.ptySize(w.PTYID)
		if dw != w.ContentWidth() || dh != w.ContentHeight() {
			t.Fatalf("pane %s: the daemon runs it at %dx%d, the clients draw it at %dx%d",
				shortID(w.PTYID), dw, dh, w.ContentWidth(), w.ContentHeight())
		}
	}
}

// twoClientsMidSizeChange is the transient no agreement can rule out: a session
// whose size has changed, where one client has heard and the other has not.
// There is a window of a few milliseconds on every resize where that is true,
// and for that window the two clients partition different boxes however well
// they agree about chrome.
//
// It is the state a sync loop starts from, and unlike a chrome disagreement it
// cannot be settled by sharing anything - the message that settles it is
// already on its way.
func twoClientsMidSizeChange(t *testing.T) (*rig, *peer, *exchange) {
	t.Helper()
	prevAnim := config.AnimationsEnabled
	config.AnimationsEnabled = false
	t.Cleanup(func() { config.AnimationsEnabled = prevAnim })

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
// peer's. It may push one thing from inside a sync - a window the daemon asked
// it to place, which is an answer rather than an echo - and that answer is
// terminal: the peer applying it has nothing left to place and so has nothing
// to say back.
//
// NEGATIVE CONTROL: measured, not assumed. Removing the applyingPeerSync guard
// in SyncStateToDaemon and letting ApplyStateSync's re-layout push - which is
// the shape of fix this area invites - turns this into an exchange that does
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

// TestFocusSwitchResizesNothing is the report in one line: switching panes
// changes no pane's size, so it must cost the guests no SIGWINCH at all.
// Every one of them narrows a pane momentarily, and a narrowing resize is
// what damages scrollback under a reflowing emulator.
//
// NEGATIVE CONTROL: measured, and it is the one that names the mechanism. On
// the unfixed tree a single pane switch resizes the two shells four times: the
// peer adopts the pushed rectangles and resizes its PTYs to them, finds the
// layout does not fill its own box, retiles, and resizes them back. Sharing the
// rail alone is not enough - that still leaves two, from the peer folding in
// chrome the pusher had not - so this is the assertion that holds the agreed
// reserve in place.
func TestFocusSwitchResizesNothing(t *testing.T) {
	r, p, ex := twoClientsDisagreeingOnChrome(t)

	var log resizeLog
	log.watch(r.m, "local")
	log.watch(p.m, "peer")

	r.m.FocusedWindow = (r.m.FocusedWindow + 1) % len(r.m.Windows)
	r.m.SyncStateToDaemon()
	ex.settle(40, 400*time.Millisecond)

	if sent := log.all(); len(sent) > 0 {
		t.Fatalf("a pane switch resized the guests %d times: %v", len(sent), sent)
	}
}
