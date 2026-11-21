package session

import (
	"testing"
	"time"
)

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

// TestAStaleSyncIsAnsweredWithTheMergedState: a client whose snapshot was
// built before a daemon-side mutation is sent the state that is canonical now,
// as a "reconcile", so it stops rendering and re-pushing its stale view.
func TestAStaleSyncIsAnsweredWithTheMergedState(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "stale")
	a := attachTestClient(t, "stale")
	seenByA := takeSyncs(a)

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
