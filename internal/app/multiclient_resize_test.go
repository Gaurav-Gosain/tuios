package app

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// A second client of a different size joining a session is the one route where
// a client has to re-lay-out without anything happening on its own end. The
// daemon's effective size is the minimum over every attached client, so the
// client already connected is the one that has to give up columns, and the
// only thing that can tell it so is the session-resize broadcast.
//
// These run on the rehydration rig: a real daemon, real client connections, and
// a real OS attached through the same entry points cmd/tuios uses.

// joinerSize is the second client's viewport: narrower and shorter than the
// rig's, so the effective size is unambiguously the joiner's.
const (
	joinerCols = 60
	joinerRows = 18
	holderCols = 120
	holderRows = 40
)

// watchSessionResize routes the client's session-resize notifications into the
// OS event channel, which is what cmd/tuios and cmd/tuios-web both do.
func (r *rig) watchSessionResize() {
	r.t.Helper()
	r.client.OnSessionResize(func(width, height, clientCount int) {
		select {
		case r.m.ClientEventChan <- ClientEvent{
			Type:        "resize",
			Width:       width,
			Height:      height,
			ClientCount: clientCount,
		}:
		default:
			r.t.Errorf("ClientEventChan full, dropped a session resize to %dx%d", width, height)
		}
	})
}

// awaitSessionResize waits for one session-resize event and applies it through
// Update, as the program loop does.
func (r *rig) awaitSessionResize(what string) SessionResizeMsg {
	r.t.Helper()
	select {
	case ev := <-r.m.ClientEventChan:
		if ev.Type != "resize" {
			r.t.Fatalf("waiting for %s: got a %q event", what, ev.Type)
		}
		msg := SessionResizeMsg{Width: ev.Width, Height: ev.Height, ClientCount: ev.ClientCount}
		r.m.Update(msg)
		return msg
	case <-time.After(rigWait):
		r.t.Fatalf("timed out waiting for %s", what)
		return SessionResizeMsg{}
	}
}

// tile puts the rig's panes side by side, which is the layout the report is
// about: the divider is where the stale width shows.
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

// joinClient attaches another client of the given size and leaves it attached
// for the rest of the test unless the caller drops it first.
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

// paneSizes reports what the client's emulators and the daemon's panes each
// think every pane's grid is, so a disagreement names the pane.
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

// waitPaneSizesAgree gives the resize the time it needs to travel the stream,
// since the client's emulator is resized by its output goroutine.
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

// TestSmallerClientJoiningResizesTheClientAlreadyThere is the maintainer's
// report: connect to a session from a second window of a different size and the
// window already connected keeps its old layout. Its panes are drawn at the new
// width while their emulators still hold what the guest wrote at the old one,
// so a line runs on past the divider.
func TestSmallerClientJoiningResizesTheClientAlreadyThere(t *testing.T) {
	r := newRigSized(t, 2, holderCols, holderRows)
	r.watchSessionResize()
	r.tile()

	before := r.m.GetRenderWidth()
	if before != holderCols {
		t.Fatalf("render width before the join is %d, want %d", before, holderCols)
	}

	joinClient(t, r.session, joinerCols, joinerRows)

	msg := r.awaitSessionResize("the session to shrink around the client already attached")
	if msg.Width != joinerCols || msg.Height != joinerRows {
		t.Fatalf("session resized to %dx%d, want the joiner's %dx%d",
			msg.Width, msg.Height, joinerCols, joinerRows)
	}
	if got := r.m.GetRenderWidth(); got != joinerCols {
		t.Fatalf("render width after the join is %d, want %d", got, joinerCols)
	}

	client, daemon, ok := r.waitPaneSizesAgree()
	if !ok {
		t.Fatalf("panes disagree after the join:\n client %v\n daemon %v", client, daemon)
	}
}

// TestLargerClientLeavingGivesTheColumnsBack is the other half: the effective
// size is the minimum over the clients, so the one that leaves hands its
// constraint back and everyone still attached has to lay out again.
func TestLargerClientLeavingGivesTheColumnsBack(t *testing.T) {
	r := newRigSized(t, 2, holderCols, holderRows)
	r.watchSessionResize()
	r.tile()

	joiner := joinClient(t, r.session, joinerCols, joinerRows)
	r.awaitSessionResize("the session to shrink around the client already attached")

	if err := joiner.Detach(); err != nil {
		t.Fatalf("second client detach: %v", err)
	}

	msg := r.awaitSessionResize("the session to grow back when the narrow client leaves")
	if msg.Width != holderCols || msg.Height != holderRows {
		t.Fatalf("session resized to %dx%d, want the remaining clients' %dx%d",
			msg.Width, msg.Height, holderCols, holderRows)
	}
	if got := r.m.GetRenderWidth(); got != holderCols {
		t.Fatalf("render width after the leave is %d, want %d", got, holderCols)
	}

	client, daemon, ok := r.waitPaneSizesAgree()
	if !ok {
		t.Fatalf("panes disagree after the leave:\n client %v\n daemon %v", client, daemon)
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

	// Put a pane out where the narrow client's edge will cut through it.
	w := r.win(0)
	w.X, w.Y = holderCols-40, 2
	w.Resize(40, 12)

	joinClient(t, r.session, joinerCols, joinerRows)
	r.awaitSessionResize("the session to shrink around the client already attached")

	// The clamp's own guarantee, the one the host terminal's resize gets: the
	// pane is no wider than the session, and enough of it is still inside the
	// content region to be grabbed. Off the right-hand edge entirely is what it
	// rules out, not hanging over it.
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

// TestDroppedClientGivesTheColumnsBack is the leave nobody announces: a browser
// tab closed, a network drop, a client killed. The connection goes away without
// a detach, and the columns it was holding down have to come back anyway.
func TestDroppedClientGivesTheColumnsBack(t *testing.T) {
	r := newRigSized(t, 2, holderCols, holderRows)
	r.watchSessionResize()
	r.tile()

	joiner := joinClient(t, r.session, joinerCols, joinerRows)
	r.awaitSessionResize("the session to shrink around the client already attached")

	if err := joiner.Close(); err != nil {
		t.Fatalf("second client close: %v", err)
	}

	msg := r.awaitSessionResize("the session to grow back when the narrow client drops")
	if msg.Width != holderCols || msg.Height != holderRows {
		t.Fatalf("session resized to %dx%d, want the remaining clients' %dx%d",
			msg.Width, msg.Height, holderCols, holderRows)
	}

	client, daemon, ok := r.waitPaneSizesAgree()
	if !ok {
		t.Fatalf("panes disagree after the drop:\n client %v\n daemon %v", client, daemon)
	}
}

// paneSpan reports the rightmost and bottommost column and row the tiled panes
// reach. A settled tiled layout fills the box tiling partitions, so a span
// short of that box is a layout computed for some other screen.
func (r *rig) paneSpan() (right, bottom int) {
	r.t.Helper()
	for _, w := range r.m.Windows {
		if w.Workspace != r.m.CurrentWorkspace || w.Minimized || w.IsFloating {
			continue
		}
		right = max(right, w.X+w.Width)
		bottom = max(bottom, w.Y+w.Height)
	}
	return right, bottom
}

// TestPeerLayoutFromASmallerClientIsRetiled is the second half of the
// maintainer's report, and the half a size broadcast alone does not fix: after
// the narrow client leaves, its last state sync is still in flight, and it
// carries the pane rectangles it computed at its own size.
//
// Adopting those leaves the panes huddled in the corner of a screen that has
// grown back around them, with the dock and the separator drawn at the full
// width. That is what "the borders don't come back" looks like on screen.
//
// NEGATIVE CONTROL: fails on the tree before tiledLayoutStale existed, where
// ApplyStateSync retiled only when a pane overflowed the viewport. A smaller
// peer's panes never overflow, so nothing corrected them and the span stayed at
// the narrow client's width.
func TestPeerLayoutFromASmallerClientIsRetiled(t *testing.T) {
	// The span is read straight off the rectangles, so a pane still easing into
	// its tile would be measured mid-flight.
	prev := config.AnimationsEnabled
	config.AnimationsEnabled = false
	defer func() { config.AnimationsEnabled = prev }()

	r := newRigSized(t, 2, holderCols, holderRows)
	r.watchSessionResize()
	r.tile()

	wantRight, wantBottom := r.paneSpan()
	if wantRight <= 0 || wantBottom <= 0 {
		t.Fatalf("the fixture has no tiled panes to measure (span %dx%d)", wantRight, wantBottom)
	}

	// A narrow client joins, so this client lays out at the narrow size.
	joiner := joinClient(t, r.session, joinerCols, joinerRows)
	r.awaitSessionResize("the session to shrink around the client already attached")
	narrow := r.m.BuildSessionState()
	if right, _ := r.paneSpan(); right >= wantRight {
		t.Fatalf("the panes still span %d columns while a %d-column client is attached; "+
			"the shrink never happened and the regrow below would prove nothing", right, joinerCols)
	}

	// It leaves, and the session grows back.
	if err := joiner.Detach(); err != nil {
		t.Fatalf("second client detach: %v", err)
	}
	r.awaitSessionResize("the session to grow back when the narrow client leaves")

	// Its last sync lands afterwards, carrying its own narrow rectangles.
	narrow.Version = r.m.DaemonStateVersion
	if err := r.m.ApplyStateSync(narrow); err != nil {
		t.Fatalf("ApplyStateSync: %v", err)
	}

	right, bottom := r.paneSpan()
	if right != wantRight || bottom != wantBottom {
		t.Errorf("after the narrow client's last sync the panes span %dx%d, want %dx%d: "+
			"this client adopted a layout computed for a screen it does not have, so its "+
			"panes and their borders sit inside an edge that has moved back out",
			right, bottom, wantRight, wantBottom)
	}
}

// TestSettledSizeIsTheSameFromBothAttachOrders pins convergence. The session
// size is the minimum over the attached clients, and a minimum does not depend
// on the order the clients arrived in.
//
// NEGATIVE CONTROL: none. This passes on the unfixed tree too, and is written
// deliberately as a property rather than a regression test: the ordering was
// already right, and this is here so a later change cannot quietly make the
// settled size depend on who attached first.
func TestSettledSizeIsTheSameFromBothAttachOrders(t *testing.T) {
	bigFirst := func() (int, int) {
		r := newRigSized(t, 1, holderCols, holderRows)
		r.watchSessionResize()
		joinClient(t, r.session, joinerCols, joinerRows)
		r.awaitSessionResize("the session to shrink around the small client")
		return r.m.GetRenderWidth(), r.m.GetRenderHeight()
	}
	// Small first, then big: the rig client is the small one. The minimum does
	// not move, so no broadcast is expected; the assertion is on what the small
	// client renders at, which must be its own size either way.
	smallFirst := func() (int, int) {
		r := newRigSized(t, 1, joinerCols, joinerRows)
		r.watchSessionResize()
		joinClient(t, r.session, holderCols, holderRows)
		// Give a broadcast that should not come the chance to arrive.
		select {
		case ev := <-r.m.ClientEventChan:
			if ev.Type == "resize" {
				r.m.Update(SessionResizeMsg{Width: ev.Width, Height: ev.Height, ClientCount: ev.ClientCount})
			}
		case <-time.After(500 * time.Millisecond):
		}
		return r.m.GetRenderWidth(), r.m.GetRenderHeight()
	}

	bw, bh := bigFirst()
	sw, sh := smallFirst()
	if bw != joinerCols || bh != joinerRows {
		t.Errorf("big-then-small settled at %dx%d, want the minimum %dx%d", bw, bh, joinerCols, joinerRows)
	}
	if sw != joinerCols || sh != joinerRows {
		t.Errorf("small-then-big settled at %dx%d, want the minimum %dx%d", sw, sh, joinerCols, joinerRows)
	}
	if bw != sw || bh != sh {
		t.Errorf("the two attach orders settled at different sizes: %dx%d and %dx%d", bw, bh, sw, sh)
	}
}

// TestAttachingAtTheMinimumIsToldTheMinimum covers the size a client is handed
// in its attach reply. It reads those dimensions as the session's effective
// size and lays its panes out at them before any broadcast arrives.
//
// NEGATIVE CONTROL: fails on the tree where handleAttach stamped the effective
// size onto the reply only when it differed from what the client asked for. A
// client attaching at the session's minimum matched, skipped the stamp, and was
// handed whatever width the last client to sync happened to render at - which
// for a local client joining a browser session is the browser's.
func TestAttachingAtTheMinimumIsToldTheMinimum(t *testing.T) {
	// The rig client is wide and syncs its wide layout to the daemon.
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
// wheel event. That is deliberate - nothing a user does may go unrecorded - and
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
// forwarded. This proves them jointly, not separately - either one alone would
// satisfy it.
//
// NEGATIVE CONTROL: fails on the tree before StateFingerprint existed. Every
// repeat is pushed and every push is forwarded, so the peer receives one sync
// per repeat.
func TestUnchangedStateIsNotDeliveredToAPeer(t *testing.T) {
	r := newRigSized(t, 2, holderCols, holderRows)
	r.tile()

	var mu sync.Mutex
	received := 0
	peer := session.NewTUIClient()
	if err := peer.Connect("test", holderCols, holderRows); err != nil {
		t.Fatalf("peer connect: %v", err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	peer.OnStateSync(func(*session.SessionState, string, string) {
		mu.Lock()
		received++
		mu.Unlock()
	})
	if _, err := peer.AttachSession(r.session, false, holderCols, holderRows); err != nil {
		t.Fatalf("peer attach: %v", err)
	}
	peer.StartReadLoop()

	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return received
	}

	// One push that does say something, so the fixture is proven to deliver at
	// all: without it a zero below would be indistinguishable from a peer that
	// never receives anything.
	r.win(0).CustomName = "renamed"
	r.m.SyncStateToDaemon()
	deadline := time.Now().Add(rigWait)
	for count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the peer never received the one sync that changed something; " +
				"the count below would prove nothing")
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	received = 0
	mu.Unlock()

	// The case under test: repeated syncs with nothing changed between them,
	// which is what typing into a pane produces.
	const repeats = 30
	for range repeats {
		r.m.SyncStateToDaemon()
	}
	// Long enough for anything forwarded to have arrived.
	time.Sleep(500 * time.Millisecond)

	if got := count(); got != 0 {
		t.Errorf("the peer was sent %d of %d syncs carrying a state it already held, want 0",
			got, repeats)
	}
}

// TestChangedStateStillReachesAPeer is the discriminating control for the test
// above, and it is written deliberately as one: a guard that suppressed
// everything would satisfy that test perfectly. It passes both before and after
// the change, and is here to say what the suppression is not allowed to do.
func TestChangedStateStillReachesAPeer(t *testing.T) {
	r := newRigSized(t, 2, holderCols, holderRows)
	r.tile()

	var mu sync.Mutex
	received := 0
	peer := session.NewTUIClient()
	if err := peer.Connect("test", holderCols, holderRows); err != nil {
		t.Fatalf("peer connect: %v", err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	peer.OnStateSync(func(*session.SessionState, string, string) {
		mu.Lock()
		received++
		mu.Unlock()
	})
	if _, err := peer.AttachSession(r.session, false, holderCols, holderRows); err != nil {
		t.Fatalf("peer attach: %v", err)
	}
	peer.StartReadLoop()

	const changes = 5
	for i := range changes {
		r.win(0).CustomName = fmt.Sprintf("name-%d", i)
		r.m.SyncStateToDaemon()
		// One at a time: the assertion is that each distinct state arrives, and
		// pushing them back to back lets the daemon coalesce two into one merge.
		time.Sleep(60 * time.Millisecond)
	}

	deadline := time.Now().Add(rigWait)
	for {
		mu.Lock()
		got := received
		mu.Unlock()
		if got >= changes {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the peer received %d of %d syncs that each changed something; "+
				"the suppression is dropping real changes", got, changes)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
