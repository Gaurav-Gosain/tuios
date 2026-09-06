package session

import (
	"testing"
	"time"
)

// The two answers a client's state sync can get, read off the wire.
//
// handleUpdateState builds the merged state only for the two paths that read
// it: the reconcile reply to a sender whose snapshot was stale, and the
// broadcast to the sender's peers. These tests are what those two paths owe,
// so that a change to when the merge is built cannot quietly hand either of
// them nothing.

// syncTaker records the state syncs a client is handed.
type syncTaker struct {
	ch chan syncSeen
}

type syncSeen struct {
	state   *SessionState
	trigger string
}

func takeSyncs(c *TUIClient) *syncTaker {
	s := &syncTaker{ch: make(chan syncSeen, 16)}
	c.OnStateSync(func(state *SessionState, trigger, _ string) {
		s.ch <- syncSeen{state: state, trigger: trigger}
	})
	return s
}

// next waits for a sync with the given trigger, skipping others.
func (s *syncTaker) next(t *testing.T, trigger string) syncSeen {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case seen := <-s.ch:
			if seen.trigger == trigger {
				return seen
			}
		case <-deadline:
			t.Fatalf("no %q state sync within 5s", trigger)
		}
	}
}

// TestPeersAreHandedTheMergedState: a sync from one client reaches the
// other as an "update" carrying the whole merged state, not nothing.
func TestPeersAreHandedTheMergedState(t *testing.T) {
	d, _ := startTestDaemon(t)
	makeSessionWithWindow(t, d, "pair")
	a := attachTestClient(t, "pair")
	b := attachTestClient(t, "pair")
	seenByB := takeSyncs(b)

	state := benchState(2)
	state.WorkspaceNames = map[int]string{1: "from-a"}
	if err := a.UpdateState(state); err != nil {
		t.Fatalf("push: %v", err)
	}
	seen := seenByB.next(t, "update")
	if seen.state == nil {
		t.Fatal("the peer was handed a state sync with no state in it")
	}
	if got := seen.state.WorkspaceNames[1]; got != "from-a" {
		t.Fatalf("the peer's state names workspace 1 %q, want the merged %q", got, "from-a")
	}
}

// TestAStaleSyncIsAnsweredWithTheMergedState: a client whose snapshot was
// built before a daemon-side mutation is sent the state that is canonical now,
// as a "reconcile", so it stops rendering and re-pushing its stale view.
func TestAStaleSyncIsAnsweredWithTheMergedState(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "stale")
	a := attachTestClient(t, "stale")
	seenByA := takeSyncs(a)

	// Two daemon-side mutations, so the session is at version 2 and a push
	// claiming to be built on version 1 is behind.
	if err := sess.SetDisplayName("first"); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetDisplayName("second"); err != nil {
		t.Fatal(err)
	}
	seenByA.next(t, "update")

	state := benchState(2)
	state.BaseVersion = 1
	if err := a.UpdateState(state); err != nil {
		t.Fatalf("push: %v", err)
	}
	seen := seenByA.next(t, "reconcile")
	if seen.state == nil {
		t.Fatal("the reconcile reply carried no state")
	}
	if seen.state.DisplayName != "second" {
		t.Fatalf("the reconcile reply names the session %q, want the daemon's %q", seen.state.DisplayName, "second")
	}
}
