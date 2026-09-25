package session

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// These tests pin who may mark an Inbox item, and the races in its wake and
// restore.

// fakeClock is a clock a test moves by hand.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// lifecycleStore is a recording store on a fake clock.
func lifecycleStore(t *testing.T) (*attentionStore, *[]streamEvent, *fakeClock) {
	t.Helper()
	a, events := recordingAttention()
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	a.now = clock.now
	t.Cleanup(func() {
		a.mu.Lock()
		if a.wakeTimer != nil {
			a.wakeTimer.Stop()
		}
		a.mu.Unlock()
	})
	return a, events, clock
}

func snooze(t *testing.T, a *attentionStore, id string, until int64) *verbError {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	_, verr := a.snoozeLocked(id, until)
	return verr
}

// TestMarkAttentionRefusesAPaneWithACopiedNonce: a live nonce is the
// person's only from outside every pane. A call a pane made, holding the
// nonce of the client attached right now, is refused as not the person.
func TestMarkAttentionRefusesAPaneWithACopiedNonce(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	tui := attachTUI(t, sp, "work")
	raw := []byte(fmt.Sprintf(`{"id":"1","action":"wake","human_nonce":%q}`, tui.HumanNonce()))
	_, verr := d.verbMarkAttention(&connState{paneOnly: true}, raw)
	if verr == nil || verr.Code != ErrVerbNotHuman {
		t.Fatalf("a pane with the person's nonce answered %v, want %s", verr, ErrVerbNotHuman)
	}
	// The same nonce from outside every pane passes the proof and reaches
	// the item, which is not there.
	_, verr = d.verbMarkAttention(&connState{}, raw)
	if verr == nil || verr.Code != ErrVerbInvalidParams {
		t.Fatalf("the person answered %v, want %s", verr, ErrVerbInvalidParams)
	}
}

// TestALateWakeFireLeavesTheNewTimer: a timer that fired while another
// goroutine held the lock and replaced it (so its Stop came too late) must
// not drop the replacement and arm a second one. It finds a newer
// generation and does nothing.
func TestALateWakeFireLeavesTheNewTimer(t *testing.T) {
	a, _, clock := lifecycleStore(t)
	a.noteSessionEvent("work", agentEvent("w1", "working", "errored", "", "a", 0, 0))
	a.noteSessionEvent("work", agentEvent("w2", "working", "errored", "", "b", 0, 0))
	items := openItems(t, a)
	now := clock.now()
	late, early := now.Add(time.Hour).UnixNano(), now.Add(time.Minute).UnixNano()
	_ = snooze(t, a, items[0].ID, late)
	a.mu.Lock()
	staleGen := a.wakeGen
	a.mu.Unlock()
	_ = snooze(t, a, items[1].ID, early)
	a.mu.Lock()
	current, at, gen := a.wakeTimer, a.wakeAt, a.wakeGen
	a.mu.Unlock()
	if current == nil || at != early || gen == staleGen {
		t.Fatalf("the second snooze left timer %v at %d gen %d", current != nil, at, gen)
	}

	// The first timer's late fire.
	a.wakeFired(staleGen)
	a.mu.Lock()
	gotTimer, gotAt, gotGen := a.wakeTimer, a.wakeAt, a.wakeGen
	a.mu.Unlock()
	if gotTimer != current || gotAt != early || gotGen != gen {
		t.Errorf("a stale fire replaced the timer: at %d gen %d, want %d gen %d", gotAt, gotGen, early, gen)
	}
	if a.snoozedCount() != 2 {
		t.Errorf("a stale fire woke items: %d asleep", a.snoozedCount())
	}
}

// TestRestoreCannotOutrunThePaneMovingOn: the check that the pane is still in
// the item's state and the reopen are one step. A transition that arrives
// between them waits for the reopen and then closes the item, so a restored
// approval is not left open for a pane that has moved on.
func TestRestoreCannotOutrunThePaneMovingOn(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, _, b := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)
	mover := dialVerb(t, sp)
	tui := attachTUI(t, sp, "work")
	nonce := tui.HumanNonce()

	setAgentState(t, c, "work", b, "needs_input", "approval", "ok?")
	items := waitAttention(t, c, "an approval", hasKind(AttentionApproval, b))
	bid := items[0]["id"].(string)
	result(t, c.call(t, fmt.Sprintf(`{"id":1,"verb":"dismiss-attention","params":{"id":%q,"human_nonce":%q}}`, bid, nonce)))
	waitAttention(t, c, "the dismiss", isEmpty)

	// The pane moves on after the check and before the reopen. The fix holds
	// the transition back until the reopen is done; without it the
	// transition lands first and finds nothing to close.
	moved := make(chan struct{})
	d.attention.mu.Lock()
	d.attention.beforeRestore = func() {
		go func() {
			defer close(moved)
			_ = mover.conn.SetDeadline(time.Now().Add(5 * time.Second * testDeadlineScale))
			if _, err := mover.conn.Write([]byte(`{"id":1,"verb":"set-agent-state","params":{"session":"work","window":"` + b + `","state":"working"}}` + "\n")); err == nil {
				_, _ = mover.r.ReadBytes('\n')
			}
		}()
		select {
		case <-moved:
		case <-time.After(300 * time.Millisecond):
		}
	}
	d.attention.mu.Unlock()
	raw, _ := json.Marshal(map[string]any{"id": bid, "action": "restore", "human_nonce": nonce})
	_ = c.call(t, fmt.Sprintf(`{"id":1,"verb":"mark-attention","params":%s}`, raw))
	<-moved
	waitAttention(t, c, "the pane moved on after the restore", isEmpty)
}
