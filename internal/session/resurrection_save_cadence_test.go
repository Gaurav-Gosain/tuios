package session

import (
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// waitForSavedState waits for a session's state file to appear, and returns
// whether it did within the deadline.
func waitForSavedState(name string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(getResurrectionPath(name)); err == nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// TestAStructuralChangeReachesDiskWithoutWaitingForTheBlindTick is the SIGKILL
// window. Saving used to be a blind 30s ticker with nothing driving it, so a
// session created 5 seconds before a SIGKILL was not merely stale, it was lost
// entirely: no state file had ever been written for it.
func TestAStructuralChangeReachesDiskWithoutWaitingForTheBlindTick(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()

	state := &SessionState{Name: "young", Windows: []WindowState{{ID: "w1"}}}
	var dirty, stale wakeLatch
	dirty.Set()
	stop := StartPeriodicSave(func() *SessionState { return state }, &dirty, &stale, nil)
	defer stop()

	if !waitForSavedState("young", resurrectionDirtyInterval*3) {
		t.Fatalf("a changed session had not reached disk after %v; the blind tick is %v away",
			resurrectionDirtyInterval*3, resurrectionInterval)
	}
	if resurrectionDirtyInterval >= resurrectionInterval {
		t.Errorf("the dirty poll (%v) is no faster than the blind tick (%v), so it narrows nothing",
			resurrectionDirtyInterval, resurrectionInterval)
	}
}

// TestAnIdleSessionDoesNotWakeTheSaver is the other half of the bargain: an
// idle session costs nothing. Nothing raised a latch, so after its first look
// the saver writes nothing and arms no timer. The saver used to poll the dirty
// flag every resurrectionDirtyInterval for ever, which this counts as 3 wakes
// in the window, and write the same state every resurrectionInterval.
//
// A raised latch still reaches disk: the second half raises dirty after the
// idle window and waits for the write.
func TestAnIdleSessionDoesNotWakeTheSaver(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()

	state := &SessionState{Name: "idle", Windows: []WindowState{{ID: "w1"}}}
	var dirty, stale wakeLatch
	var wakes atomic.Int64
	stop := StartPeriodicSave(func() *SessionState { return state }, &dirty, &stale, &wakes)
	defer stop()

	// The first look, then three more intervals of nothing.
	time.Sleep(resurrectionDirtyInterval*4 + resurrectionDirtyInterval/2)
	if waitForSavedState("idle", 0) {
		t.Error("an unchanged session was written; an idle session is meant to cost nothing")
	}
	if n := wakes.Load(); n > 1 {
		t.Errorf("the saver woke %d times over an idle session, want at most the first look", n)
	}

	dirty.Set()
	if !waitForSavedState("idle", resurrectionDirtyInterval*3) {
		t.Fatal("a structural change after an idle spell did not reach disk")
	}
}

// TestSessionMutationsMarkTheStateDirty pins the wiring: the saver only helps if
// the operations that change a session actually raise the flag it reads.
func TestSessionMutationsMarkTheStateDirty(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()

	sess := newTestSession(t)

	take := func() bool { return sess.stateDirty.Take() }
	take() // clear whatever creation left

	if err := sess.SetDisplayName("labelled"); err != nil {
		t.Fatalf("SetDisplayName: %v", err)
	}
	if !take() {
		t.Error("a daemon-side mutation did not mark the state dirty, so it waits for the blind tick")
	}

	sess.UpdateState(sess.GetState())
	if !take() {
		t.Error("a client state sync did not mark the state dirty")
	}

	sess.SetOption("k", "v")
	if !take() {
		t.Error("setting a session option did not mark the state dirty")
	}

	if take() {
		t.Error("the dirty mark was not consumed by reading it, so every poll would write")
	}
}
