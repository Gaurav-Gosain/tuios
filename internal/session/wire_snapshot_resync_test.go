package session

import (
	"bytes"
	"testing"
)

// TestWireFromSnapshotSkipsTheResyncWhenRolled pins the production half of the
// issue #123 fix: the FromSnapshot flag the app sends after restoring a
// snapshot must reach the daemon and route the subscribe through
// SubscribeFromSnapshot. With the flag set, a rolled catch-up (the ring has
// moved past the client's position) must replay the tail without the resync
// clear that would throw the restored snapshot away; without the flag, the
// clear must still be there for a client resuming a screen it drew byte by
// byte.
//
// The branch is what switches the fix on in production. Make
// daemon_handlers.go's FromSnapshot branch unreachable, or make the app pass
// false, and this test fails.
func TestWireFromSnapshotSkipsTheResyncWhenRolled(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "wire-snapshot")
	ptyID := sess.ListPTYIDs()[0]
	pty := sess.GetPTY(ptyID)
	client := attachTestClient(t, "wire-snapshot")

	// Roll the ring well past any position the client could name, so any
	// subscribe from a small fromSeq is a rolled catch-up.
	pty.appendAndBroadcast(bytes.Repeat([]byte("x"), 96*1024))

	// FromSnapshot=true: a client that just laid down a snapshot. The replay
	// must not open with the resync clear.
	var snap collector
	if err := client.SubscribePTY(ptyID, 1, true, snap.add); err != nil {
		t.Fatalf("subscribe with FromSnapshot: %v", err)
	}
	waitFor(t, "the snapshot replay to arrive", func() bool {
		snap.mu.Lock()
		defer snap.mu.Unlock()
		return len(snap.buf) > 0
	})
	if got := snap.take(); bytes.HasPrefix(got, resyncPrefix) {
		t.Errorf("a client that restored a snapshot was handed the resync clear (%d bytes): the FromSnapshot branch is not live", len(got))
	}

	client.UnsubscribePTY(ptyID)
	waitFor(t, "the daemon to drop the subscription", func() bool {
		pty.subscribersMu.RLock()
		defer pty.subscribersMu.RUnlock()
		return len(pty.subscribers) == 0
	})

	// FromSnapshot=false: the old contract for a client resuming a screen it
	// drew itself. The rolled replay keeps its resync clear.
	var plain collector
	if err := client.SubscribePTY(ptyID, 1, false, plain.add); err != nil {
		t.Fatalf("subscribe without FromSnapshot: %v", err)
	}
	waitFor(t, "the plain replay to arrive", func() bool {
		plain.mu.Lock()
		defer plain.mu.Unlock()
		return len(plain.buf) > 0
	})
	if got := plain.take(); !bytes.HasPrefix(got, resyncPrefix) {
		t.Errorf("a plain rolled resubscribe lost its resync clear (%d bytes)", len(got))
	}
}
