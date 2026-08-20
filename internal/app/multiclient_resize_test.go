package app

import (
	"fmt"
	"testing"
	"time"

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
