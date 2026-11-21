package app

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const (
	joinerCols = 60
	joinerRows = 18
	holderCols = 120
	holderRows = 40
)

func (r *rig) watchSessionResize() {
	r.t.Helper()
	r.client.OnSessionResize(func(width, height, clientCount int, reserve session.LayoutReserve) {
		select {
		case r.m.ClientEventChan <- ClientEvent{
			Type:        "resize",
			Width:       width,
			Height:      height,
			ClientCount: clientCount,
			Reserve:     reserve,
			Generation:  r.client.SessionLayoutGeneration(),
		}:
		default:
			r.t.Errorf("ClientEventChan full, dropped a session resize to %dx%d", width, height)
		}
	})
}

func (r *rig) awaitSessionResize(what string) SessionResizeMsg {
	r.t.Helper()
	fromW, fromH := r.m.GetRenderWidth(), r.m.GetRenderHeight()
	deadline := time.After(rigWait)
	for {
		select {
		case ev := <-r.m.ClientEventChan:
			if ev.Type != "resize" {
				r.t.Fatalf("waiting for %s: got a %q event", what, ev.Type)
			}
			msg := clientEventMsg(ev).(SessionResizeMsg)
			r.m.Update(msg)
			if msg.Width != fromW || msg.Height != fromH {
				return msg
			}
		case <-deadline:
			r.t.Fatalf("timed out waiting for %s", what)
			return SessionResizeMsg{}
		}
	}
}

func (r *rig) tile() {
	r.t.Helper()
	r.m.AutoTiling = true
	r.m.TileAllWindows()
	r.m.SyncDaemonPTYDimensions()
	if _, _, ok := r.waitPaneSizesAgree(); !ok {
		client, daemon := r.paneSizes()
		r.t.Fatalf("panes disagree before the test starts:\n client %v\n daemon %v", client, daemon)
	}
}

func joinClient(t *testing.T, name string, width, height int) *session.TUIClient {
	t.Helper()
	c := session.NewTUIClient()
	if err := c.Connect("test", width, height); err != nil {
		t.Fatalf("second client connect: %v", err)
	}
	if _, err := c.AttachSession(name, false, width, height); err != nil {
		t.Fatalf("second client attach: %v", err)
	}
	c.StartReadLoop()
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func (r *rig) paneSizes() (client, daemon []string) {
	r.t.Helper()
	for _, w := range r.m.Windows {
		w.RLockIO()
		cw, ch := 0, 0
		if w.Terminal != nil {
			cw, ch = w.Terminal.Width(), w.Terminal.Height()
		}
		w.RUnlockIO()
		client = append(client, fmt.Sprintf("%s=%dx%d", shortID(w.PTYID), cw, ch))
		dw, dh := r.ptySize(w.PTYID)
		daemon = append(daemon, fmt.Sprintf("%s=%dx%d", shortID(w.PTYID), dw, dh))
	}
	return client, daemon
}

func (r *rig) waitPaneSizesAgree() (client, daemon []string, ok bool) {
	r.t.Helper()
	deadline := time.Now().Add(rigWait)
	for {
		client, daemon = r.paneSizes()
		if fmt.Sprint(client) == fmt.Sprint(daemon) {
			return client, daemon, true
		}
		if time.Now().After(deadline) {
			return client, daemon, false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestFloatingPanesAreClampedWhenAnotherClientShrinksTheSession covers the
// layout that has no retile to fall back on. A floating pane keeps its own
// geometry, so nothing pulls it back inside an edge that moved in because
// somebody else attached from a smaller window.
func TestFloatingPanesAreClampedWhenAnotherClientShrinksTheSession(t *testing.T) {
	r := newRigSized(t, 2, holderCols, holderRows)
	r.watchSessionResize()
	if r.m.AutoTiling {
		t.Fatalf("the rig came up tiling; this test is about the floating layout")
	}

	w := r.win(0)
	w.X, w.Y = holderCols-40, 2
	w.Resize(40, 12)

	joinClient(t, r.session, joinerCols, joinerRows)
	r.awaitSessionResize("the session to shrink around the client already attached")

	rightEdge := r.m.GetLeftMargin() + r.m.GetContentWidth()
	if w.Width > r.m.GetContentWidth() {
		t.Fatalf("pane is %d columns wide, past the %d the session now has",
			w.Width, r.m.GetContentWidth())
	}
	if w.X >= rightEdge {
		t.Fatalf("pane starts at column %d, off the right of the %d the session now has",
			w.X, rightEdge)
	}
	if w.Height > r.m.GetUsableHeight() {
		t.Fatalf("pane is %d rows tall, past the %d the session now has",
			w.Height, r.m.GetUsableHeight())
	}
	if w.Y >= r.m.GetTopMargin()+r.m.GetUsableHeight() {
		t.Fatalf("pane starts at row %d, below the %d the session now has",
			w.Y, r.m.GetTopMargin()+r.m.GetUsableHeight())
	}
}

// TestAttachingAtTheMinimumIsToldTheMinimum covers the size a client is handed
// in its attach reply. It reads those dimensions as the session's effective
// size and lays its panes out at them before any broadcast arrives.
//
// NEGATIVE CONTROL: fails on the tree where handleAttach stamped the effective
// size onto the reply only when it differed from what the client asked for. A
// client attaching at the session's minimum matched, skipped the stamp, and was
// handed whatever width the last client to sync happened to render at, which
// for a local client joining a browser session is the browser's.
func TestAttachingAtTheMinimumIsToldTheMinimum(t *testing.T) {
	r := newRigSized(t, 2, holderCols, holderRows)
	r.tile()
	r.m.SyncStateToDaemon()

	c := session.NewTUIClient()
	if err := c.Connect("test", joinerCols, joinerRows); err != nil {
		t.Fatalf("narrow client connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	state, err := c.AttachSession(r.session, false, joinerCols, joinerRows)
	if err != nil {
		t.Fatalf("narrow client attach: %v", err)
	}
	if state == nil {
		t.Fatal("attach returned no state")
	}
	if state.Width != joinerCols || state.Height != joinerRows {
		t.Errorf("the attach reply says the session is %dx%d, want the effective %dx%d: "+
			"this client renders at that until a broadcast corrects it, and no broadcast "+
			"is sent when the minimum did not move",
			state.Width, state.Height, joinerCols, joinerRows)
	}
}

// TestUnchangedStateIsNotDeliveredToAPeer is the "it recalculates stuff and
// resizes as you interact" half of the report, seen from the wire.
//
// A client pushes its whole state after every keystroke, every click and every
// wheel event. That is deliberate (nothing a user does may go unrecorded), and
// it means the great majority of pushes say exactly what the last one said.
// Measured on the unfixed tree with two clients attached, thirty-one keystrokes
// produced thirty-two peer broadcasts and all thirty-two carried a state
// identical to the one before. Each one costs the peer a full state
// application and a redraw, which is what one client's typing did to another's
// screen.
//
// The assertion is on what reaches the peer, which is where the cost lands.
// Two guards stand between the keystroke and that: the client does not send a
// state it has already sent, and the daemon does not forward one it has already
// forwarded. This proves them jointly, not separately: either one alone would
// satisfy it.
//
// NEGATIVE CONTROL: fails on the tree before StateFingerprint existed. Every
// repeat is pushed and every push is forwarded, so the peer receives one sync
// per repeat.
func TestUnchangedStateIsNotDeliveredToAPeer(t *testing.T) {
	r := newRigSized(t, 2, holderCols, holderRows)
	r.tile()

	var mu sync.Mutex
	var names []string
	peer := session.NewTUIClient()
	if err := peer.Connect("test", holderCols, holderRows); err != nil {
		t.Fatalf("peer connect: %v", err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	peer.OnStateSync(func(st *session.SessionState, _, _ string) {
		name := ""
		if st != nil {
			for _, w := range st.Windows {
				name += w.CustomName + "\x00"
			}
		}
		mu.Lock()
		names = append(names, name)
		mu.Unlock()
	})
	if _, err := peer.AttachSession(r.session, false, holderCols, holderRows); err != nil {
		t.Fatalf("peer attach: %v", err)
	}
	peer.StartReadLoop()

	syncsUntil := func(name, what string) int {
		t.Helper()
		r.win(0).CustomName = name
		r.m.SyncStateToDaemon()
		deadline := time.Now().Add(rigWait)
		for {
			mu.Lock()
			for i, got := range names {
				if strings.Contains(got, name+"\x00") {
					before := i
					names = names[i+1:]
					mu.Unlock()
					return before
				}
			}
			mu.Unlock()
			if time.Now().After(deadline) {
				t.Fatalf("the peer never received %s", what)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	_ = syncsUntil("renamed", "the one sync that changed something; the count below would prove nothing")

	const repeats = 30
	for range repeats {
		r.m.SyncStateToDaemon()
	}

	if got := syncsUntil("barrier", "the rename that marks the end of the repeats"); got != 0 {
		t.Errorf("the peer was sent %d of %d syncs carrying a state it already held, want 0",
			got, repeats)
	}
}
