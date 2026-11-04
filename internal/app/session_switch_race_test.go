package app

import (
	"testing"
	"time"
)

// Deterministic race regressions for a session switch and for a peer's sync
// during a drag. Each runs on the rehydration rig: a real daemon, a real
// client connection wired by WireDaemonClient, and a real OS. The race is made
// deterministic by holding a message the read loop has already queued, doing
// the thing it raced, and only then handing the message to Update, which is
// what a busy Update goroutine does to it.

// awaitClientEvent takes events off the client event queue until one matches,
// and returns it without applying it.
func (r *rig) awaitClientEvent(what string, match func(ClientEvent) bool) ClientEvent {
	r.t.Helper()
	deadline := time.After(rigWait)
	for {
		select {
		case ev := <-r.m.ClientEventChan:
			if match(ev) {
				return ev
			}
		case <-deadline:
			r.t.Fatalf("timed out waiting for %s", what)
			return ClientEvent{}
		}
	}
}

// awaitStateSync takes states off the sync queue until one matches, and
// returns it without applying it.
func (r *rig) awaitStateSync(what string, match func(StateSyncMsg) bool) StateSyncMsg {
	r.t.Helper()
	deadline := time.After(rigWait)
	for {
		select {
		case msg := <-r.m.StateSyncChan:
			if msg.State != nil && match(msg) {
				return msg
			}
		case <-deadline:
			r.t.Fatalf("timed out waiting for %s", what)
			return StateSyncMsg{}
		}
	}
}

func windowIDs(m *OS) []string {
	ids := make([]string, 0, len(m.Windows))
	for _, w := range m.Windows {
		ids = append(ids, w.ID)
	}
	return ids
}

// TestSessionSwitchDropsTheLeftSessionsResize: a session resize of the session
// left, read before the switch and handled after it, was applied to the
// session joined. It laid that session's panes out in the other session's box
// and raised layoutGenApplied past the joined session's own count, so the
// daemon's stale-layout check took this client's pushes for current.
func TestSessionSwitchDropsTheLeftSessionsResize(t *testing.T) {
	r := newRig(t, 1)
	r.m.WireDaemonClient(r.client)
	other := r.otherSession()

	// A smaller client joins the first session, and the session shrinks to it.
	joinClient(t, r.session, joinerCols, joinerRows)
	held := r.awaitClientEvent("the first session's resize", func(ev ClientEvent) bool {
		return ev.Type == "resize" && ev.Width == joinerCols
	})

	if err := r.m.SwitchToSession(other); err != nil {
		t.Fatalf("switch: %v", err)
	}
	wantW, wantH := r.m.EffectiveWidth, r.m.EffectiveHeight
	wantGen := r.m.layoutGenApplied
	if wantW == joinerCols {
		t.Fatalf("the second session is %dx%d, the joiner's size; the test cannot tell the two apart", wantW, wantH)
	}
	if held.Generation <= wantGen {
		t.Fatalf("the held resize has generation %d, not above the second session's %d; the test cannot see it raised", held.Generation, wantGen)
	}

	r.m.Update(clientEventMsg(held))

	if r.m.EffectiveWidth != wantW || r.m.EffectiveHeight != wantH {
		t.Errorf("after the first session's resize, the second session is laid out at %dx%d, want its own %dx%d",
			r.m.EffectiveWidth, r.m.EffectiveHeight, wantW, wantH)
	}
	if r.m.layoutGenApplied != wantGen {
		t.Errorf("layoutGenApplied = %d after the first session's resize, want the second session's %d",
			r.m.layoutGenApplied, wantGen)
	}
}

// TestPeerSyncDuringDragFollowsThePane: a peer's sync rebuilds m.Windows in
// the daemon's order. The drag held its pane by index, so a peer closing a
// pane before it moved the drag onto the pane after it. A peer closing the
// dragged pane itself left a drag with nothing under the pointer.
func TestPeerSyncDuringDragFollowsThePane(t *testing.T) {
	r := newRig(t, 3)
	r.m.WireDaemonClient(r.client)

	// The drag and the scrollbar grab, as a click on a pane's scrollbar
	// starts them.
	dragged := r.m.Windows[1].ID
	r.m.Dragging = true
	r.m.DraggedWindowIndex = 1
	r.m.ScrollbarDragging = true
	r.m.ScrollbarDragWindowIndex = 1

	// A peer closes the pane before the dragged one.
	if err := r.ctl.SendIntent("CloseWindow", r.m.Windows[0].ID); err != nil {
		t.Fatalf("close window: %v", err)
	}
	r.m.Update(r.awaitStateSync("the state with two panes", func(msg StateSyncMsg) bool {
		return len(msg.State.Windows) == 2
	}))

	if i := r.m.DraggedWindowIndex; i < 0 || i >= len(r.m.Windows) || r.m.Windows[i].ID != dragged {
		t.Errorf("after a peer closed another pane, the drag holds index %d of %v, want the pane %s",
			i, windowIDs(r.m), dragged)
	}
	if i := r.m.ScrollbarDragWindowIndex; i < 0 || i >= len(r.m.Windows) || r.m.Windows[i].ID != dragged {
		t.Errorf("after a peer closed another pane, the scrollbar grab holds index %d of %v, want the pane %s",
			i, windowIDs(r.m), dragged)
	}

	// A peer closes the dragged pane.
	if err := r.ctl.SendIntent("CloseWindow", dragged); err != nil {
		t.Fatalf("close window: %v", err)
	}
	r.m.Update(r.awaitStateSync("the state with one pane", func(msg StateSyncMsg) bool {
		return len(msg.State.Windows) == 1
	}))

	if r.m.Dragging || r.m.DraggedWindowIndex != -1 || r.m.ScrollbarDragging || r.m.ScrollbarDragWindowIndex != -1 {
		t.Errorf("after a peer closed the dragged pane, dragging=%v index=%d scrollbar=%v index=%d; want the gesture ended",
			r.m.Dragging, r.m.DraggedWindowIndex, r.m.ScrollbarDragging, r.m.ScrollbarDragWindowIndex)
	}
}
