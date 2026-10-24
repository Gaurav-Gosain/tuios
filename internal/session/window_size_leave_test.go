package session

import (
	"testing"
	"time"
)

// latestPair is a session with a 160x48 client that typed last and an idle
// 100x30 one, under the latest policy, settled at the big client's size.
func latestPair(t *testing.T) (d *Daemon, sess *Session, big, small *connState) {
	t.Helper()
	d, sess = newTestDaemonSession(t)
	small = addSizedClient(d, "small", sess.ID, 100, 30)
	big = addSizedClient(d, "big", sess.ID, 160, 48)
	for _, cs := range []*connState{small, big} {
		cs.windowSizeCap = true
	}
	if err := d.handleClientActivity(big); err != nil {
		t.Fatal(err)
	}
	d.recalculateAndBroadcastSize(sess.ID, "")
	if w, h := sess.Size(); w != 160 || h != 48 {
		t.Fatalf("with the big client latest the session is %dx%d, want 160x48", w, h)
	}
	return d, sess, big, small
}

// TestLatestLeavingKeepsSize is the report: a person types at one client,
// switches it to another session and back. The other client of the session
// had no input in between, so the session must keep the size it had, and not
// take the idle client's and give it back.
//
// NEGATIVE CONTROL: with pickLatest handing the session to the client that
// remains whatever its input, the session is 100x30 once the big client has
// left, and the first check fails.
func TestLatestLeavingKeepsSize(t *testing.T) {
	d, sess, big, _ := latestPair(t)
	gen := sess.LayoutGeneration()
	reserve := sess.LayoutReserve()

	if !d.detachClient(big) {
		t.Fatal("the big client was not attached")
	}
	if w, h := sess.Size(); w != 160 || h != 48 {
		t.Fatalf("after the latest client left the session is %dx%d, want 160x48 kept", w, h)
	}
	if got := sess.LayoutGeneration(); got != gen {
		t.Errorf("the layout moved on (generation %d, was %d) with nothing to change", got, gen)
	}
	if got := sess.LayoutReserve(); got != reserve {
		t.Errorf("the reserve is %+v, was %+v", got, reserve)
	}

	// Back, as a session switch attaches it again: still 160x48, and nothing
	// was resized on the way.
	big.mu.Lock()
	big.sessionID, big.width, big.height = sess.ID, 160, 48
	big.mu.Unlock()
	d.recalculateAndBroadcastSize(sess.ID, big.clientID)
	if w, h := sess.Size(); w != 160 || h != 48 {
		t.Fatalf("with the big client back the session is %dx%d, want 160x48", w, h)
	}
	if got := sess.LayoutGeneration(); got != gen {
		t.Errorf("the layout moved on (generation %d, was %d) over the round trip", got, gen)
	}
}

// TestLatestLeavingThenInputHandsOver holds the other side: once the client
// still attached has input, it takes the session, as before.
func TestLatestLeavingThenInputHandsOver(t *testing.T) {
	d, sess, big, small := latestPair(t)
	if !d.detachClient(big) {
		t.Fatal("the big client was not attached")
	}
	time.Sleep(activityRecalcGap)
	if err := d.handleClientActivity(small); err != nil {
		t.Fatal(err)
	}
	if w, h := sess.Size(); w != 100 || h != 30 {
		t.Fatalf("after input in the client still attached the session is %dx%d, want its 100x30", w, h)
	}
}

// TestLatestLeavingWithoutInputHandsOver holds the old rule where nobody had
// input: a latest client that never typed leaves, and the session takes the
// client that remains, as tmux does.
func TestLatestLeavingWithoutInputHandsOver(t *testing.T) {
	d, sess := newTestDaemonSession(t)
	small := addSizedClient(d, "small", sess.ID, 100, 30)
	big := addSizedClient(d, "big", sess.ID, 160, 48)
	for _, cs := range []*connState{small, big} {
		cs.windowSizeCap = true
	}
	big.attachSeq, small.attachSeq = 2, 1
	d.recalculateAndBroadcastSize(sess.ID, "")
	if w, h := sess.Size(); w != 160 || h != 48 {
		t.Fatalf("with the big client attached last the session is %dx%d, want 160x48", w, h)
	}
	if !d.detachClient(big) {
		t.Fatal("the big client was not attached")
	}
	if w, h := sess.Size(); w != 100 || h != 30 {
		t.Fatalf("after a latest client with no input left the session is %dx%d, want 100x30", w, h)
	}
}
