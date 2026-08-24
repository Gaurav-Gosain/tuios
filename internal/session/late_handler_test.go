package session

import (
	"testing"
)

// The read loop starts before the handlers exist: cmd/tuios attaches, starts
// reading, builds the program, and only then registers OnStateSync and
// OnSessionResize. A broadcast landing in that window used to be dropped on
// the floor, and both messages are last-value-wins snapshots the daemon does
// not resend until something changes - so a peer's push racing this client's
// attach was simply lost, and the two clients sat on different state until
// the peer's next change. These pin the retention: the newest missed
// broadcast of each kind is handed to the handler at registration.

// deliver runs one fabricated broadcast through the client's own dispatch.
func deliver(t *testing.T, c *TUIClient, msgType MessageType, payload any) {
	t.Helper()
	msg, err := NewMessageWithCodec(msgType, payload, c.codec)
	if err != nil {
		t.Fatalf("build message: %v", err)
	}
	c.handleMessage(msg)
}

// TestStateSyncBeforeHandlerIsDeliveredOnRegistration is the state half.
//
// NEGATIVE CONTROL: measured. Without the retention in handleMessage the
// registered handler is never called and the test fails with "a state sync
// that arrived before the handler was registered was dropped". With a handler
// already registered the pending path is never taken, which the second half
// asserts by counting.
func TestStateSyncBeforeHandlerIsDeliveredOnRegistration(t *testing.T) {
	c := NewTUIClient()

	deliver(t, c, MsgStateSync, StateSyncPayload{
		State:       &SessionState{Name: "early"},
		TriggerType: "test",
		SourceID:    "peer",
	})
	// Only the newest is kept: a second sync supersedes the first outright,
	// because the state is a whole snapshot rather than a delta.
	deliver(t, c, MsgStateSync, StateSyncPayload{
		State:       &SessionState{Name: "newest"},
		TriggerType: "test",
		SourceID:    "peer",
	})

	var got []string
	c.OnStateSync(func(state *SessionState, _, _ string) {
		got = append(got, state.Name)
	})
	if len(got) != 1 || got[0] != "newest" {
		t.Fatalf("a state sync that arrived before the handler was registered was dropped or replayed wrong: got %v, want [newest]", got)
	}

	// With the handler in place, delivery is direct and nothing is parked: a
	// re-registration hands over nothing extra.
	deliver(t, c, MsgStateSync, StateSyncPayload{
		State:       &SessionState{Name: "live"},
		TriggerType: "test",
		SourceID:    "peer",
	})
	if len(got) != 2 || got[1] != "live" {
		t.Fatalf("a sync with a live handler went astray: got %v", got)
	}
	c.OnStateSync(func(state *SessionState, _, _ string) {
		got = append(got, "again:"+state.Name)
	})
	if len(got) != 2 {
		t.Fatalf("re-registering replayed a sync that was already delivered: got %v", got)
	}
}

// TestSessionResizeBeforeHandlerIsDeliveredOnRegistration is the box half:
// the resize message carries the size and reserve the panes are laid out in,
// and a client that misses it partitions a box the session has moved on from.
//
// NEGATIVE CONTROL: measured. Without the retention the handler never fires
// and the test fails at the first assertion.
func TestSessionResizeBeforeHandlerIsDeliveredOnRegistration(t *testing.T) {
	c := NewTUIClient()

	deliver(t, c, MsgSessionResize, SessionResizePayload{
		Width: 100, Height: 30, ClientCount: 2,
		Reserve:    LayoutReserve{Left: 3},
		Generation: 1,
	})
	deliver(t, c, MsgSessionResize, SessionResizePayload{
		Width: 90, Height: 28, ClientCount: 2,
		Reserve:    LayoutReserve{Left: 5},
		Generation: 2,
	})

	type box struct {
		w, h int
		r    LayoutReserve
	}
	var got []box
	c.OnSessionResize(func(width, height, _ int, reserve LayoutReserve) {
		got = append(got, box{width, height, reserve})
	})
	if len(got) != 1 || got[0] != (box{90, 28, LayoutReserve{Left: 5}}) {
		t.Fatalf("a session resize that arrived before the handler was registered was dropped or replayed wrong: got %v", got)
	}
	// The reserve itself was recorded on arrival either way; the retention is
	// about the notification, and the two must agree.
	if r := c.SessionLayoutReserve(); r != (LayoutReserve{Left: 5}) {
		t.Fatalf("the recorded reserve %+v does not match the replayed one", r)
	}
}
