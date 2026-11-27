package session

import (
	"maps"
	"testing"
	"time"
)

// TestAStateFromBeforeAnyPushIsRefusedAfterOwnPush: a client attaches and
// starts reading before it registers a state handler, as cmd/tuios does. The
// daemon broadcasts a change before any client of the session has pushed, so
// the state carries an empty push table, and the client keeps it for the
// handler. The client then pushes. When the handler registers, the kept state
// is older than the push and must be refused.
//
// Negative control: with PredatesOwnPush reading a nil PushSeen as a daemon
// that keeps no table, whatever PushTable says, the client takes the state.
func TestAStateFromBeforeAnyPushIsRefusedAfterOwnPush(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "early")
	c := attachTUI(t, sp, "early")
	c.StartReadLoop()
	sess := d.manager.GetSession("early")

	publishMarker(t, sess)
	deadline := time.Now().Add(5 * time.Second * testDeadlineScale)
	for {
		c.multiClientMu.RLock()
		kept := c.pendingStateSync != nil
		c.multiClientMu.RUnlock()
		if kept {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the client kept no state for its handler, so this test proves nothing")
		}
		time.Sleep(5 * time.Millisecond)
	}

	pushed := sess.GetState()
	opts := maps.Clone(pushed.Options)
	opts["own-push"] = "1"
	pushed.Options = opts
	pushed.BaseVersion = pushed.Version
	pushed.PushSeen, pushed.SnapshotSeq = nil, 0
	if err := c.UpdateState(pushed); err != nil {
		t.Fatalf("push: %v", err)
	}
	for sess.GetState().Options["own-push"] != "1" {
		if time.Now().After(deadline) {
			t.Fatal("the daemon never took the push")
		}
		time.Sleep(5 * time.Millisecond)
	}

	var kept *SessionState
	c.OnStateSync(func(s *SessionState, _, _ string) {
		if kept == nil {
			kept = s
		}
	})
	if kept == nil {
		t.Fatal("the kept state was not handed to the handler")
	}
	if kept.Options["own-push"] == "1" {
		t.Fatal("the kept state already holds the push, so this test proves nothing")
	}
	if !kept.PushTable || kept.PushSeen != nil {
		t.Fatalf("the kept state has PushTable=%v and push table %v, want the flag and an empty table", kept.PushTable, kept.PushSeen)
	}
	if c.AcceptState(kept) {
		t.Fatal("the client takes a state built before its own push: it undoes the push on this client alone")
	}
	// A daemon that keeps no table sends neither, and its states are taken
	// as they always were.
	old := *kept
	old.PushTable = false
	if c.PredatesOwnPush(&old) {
		t.Fatal("a state from a daemon with no push table is refused")
	}
}
