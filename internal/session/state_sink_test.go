package session

import (
	"sync"
	"testing"
)

// recordStateSink installs a state sink on sess and returns a getter for the
// snapshots it received, in delivery order.
func recordStateSink(sess *Session) func() []*SessionState {
	var mu sync.Mutex
	var got []*SessionState
	sess.SetStateSink(func(state *SessionState) {
		mu.Lock()
		got = append(got, state)
		mu.Unlock()
	})
	return func() []*SessionState {
		mu.Lock()
		defer mu.Unlock()
		return append([]*SessionState(nil), got...)
	}
}

// TestDaemonMutationPushesStateToSink is the whole point of the state sink: a
// mutation the daemon makes itself is announced, so an attached client learns
// about it without the operation knowing a client exists.
func TestDaemonMutationPushesStateToSink(t *testing.T) {
	sess := newTestSession(t)
	pushes := recordStateSink(sess)

	win, err := sess.AddDaemonWindow("shell", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow failed: %v", err)
	}

	got := pushes()
	if len(got) != 1 {
		t.Fatalf("push count = %d, want 1", len(got))
	}
	if len(got[0].Windows) != 1 || got[0].Windows[0].ID != win.ID {
		t.Fatalf("pushed state does not carry the created window: %+v", got[0].Windows)
	}
	if got[0].Version != sess.GetState().Version {
		t.Errorf("pushed version = %d, want %d", got[0].Version, sess.GetState().Version)
	}
}

// TestStatePushCarriesEveryDaemonMutation pins that the push is not special-cased
// per operation: rename, minimize, and workspace switch each announce themselves
// because they all run through mutateState.
func TestStatePushCarriesEveryDaemonMutation(t *testing.T) {
	sess := newTestSession(t)
	win, err := sess.AddDaemonWindow("shell", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow failed: %v", err)
	}

	pushes := recordStateSink(sess)

	if err := sess.RenameDaemonWindow(win.ID, "editor"); err != nil {
		t.Fatalf("RenameDaemonWindow failed: %v", err)
	}
	if err := sess.SetDaemonWindowMinimized(win.ID, true); err != nil {
		t.Fatalf("SetDaemonWindowMinimized failed: %v", err)
	}
	if err := sess.SwitchDaemonWorkspace(3); err != nil {
		t.Fatalf("SwitchDaemonWorkspace failed: %v", err)
	}

	got := pushes()
	if len(got) != 3 {
		t.Fatalf("push count = %d, want 3", len(got))
	}
	if got[0].Windows[0].CustomName != "editor" {
		t.Errorf("rename push carries CustomName %q, want editor", got[0].Windows[0].CustomName)
	}
	if !got[1].Windows[0].Minimized {
		t.Error("minimize push does not carry Minimized")
	}
	if got[2].CurrentWorkspace != 3 {
		t.Errorf("workspace push carries workspace %d, want 3", got[2].CurrentWorkspace)
	}

	// Versions are strictly increasing, so a client can tell the order it was
	// told things in from the payload alone.
	for i := 1; i < len(got); i++ {
		if got[i].Version <= got[i-1].Version {
			t.Fatalf("push %d version %d does not advance past %d", i, got[i].Version, got[i-1].Version)
		}
	}
}

// TestFailedMutationPushesNothing keeps the sink honest: a client must not be
// asked to re-render for an operation that changed nothing.
func TestFailedMutationPushesNothing(t *testing.T) {
	sess := newTestSession(t)
	pushes := recordStateSink(sess)

	if err := sess.RenameDaemonWindow("no-such-window", "x"); err == nil {
		t.Fatal("RenameDaemonWindow on a missing target should fail")
	}
	if got := pushes(); len(got) != 0 {
		t.Fatalf("push count = %d, want 0", len(got))
	}
}

// TestClientSyncDoesNotPushToSink pins that a client's own UpdateState does not
// come back at it through the sink. The daemon already answers a sync directly
// (reconciled state to the sender, a broadcast to peers); pushing from here too
// would be a second, unordered copy of the same news.
func TestClientSyncDoesNotPushToSink(t *testing.T) {
	sess := newTestSession(t)
	pushes := recordStateSink(sess)

	state := sess.GetState()
	state.BaseVersion = state.Version
	state.CurrentWorkspace = 2
	sess.UpdateState(state)

	if got := pushes(); len(got) != 0 {
		t.Fatalf("push count = %d, want 0", len(got))
	}
}

// TestLateSnapshotIsDropped pins the ordering guarantee directly. The snapshot is
// taken under the state lock but delivered without it, so two concurrent
// mutations can reach publishState in either order. A client must never be handed
// a state older than one it has already applied, so the loser is dropped rather
// than delivered.
func TestLateSnapshotIsDropped(t *testing.T) {
	sess := newTestSession(t)
	pushes := recordStateSink(sess)

	sess.publishState(&SessionState{Version: 7, CurrentWorkspace: 7})
	sess.publishState(&SessionState{Version: 5, CurrentWorkspace: 5}) // overtaken
	sess.publishState(&SessionState{Version: 7, CurrentWorkspace: 9}) // already seen
	sess.publishState(&SessionState{Version: 8, CurrentWorkspace: 8})

	got := pushes()
	if len(got) != 2 {
		t.Fatalf("push count = %d, want 2", len(got))
	}
	if got[0].Version != 7 || got[1].Version != 8 {
		t.Fatalf("delivered versions = %d, %d; want 7, 8", got[0].Version, got[1].Version)
	}
}

// TestConcurrentMutationsPushInVersionOrder runs the real path under -race: many
// mutations at once must leave the sink with a strictly increasing version
// sequence ending on the session's final state.
func TestConcurrentMutationsPushInVersionOrder(t *testing.T) {
	sess := newTestSession(t)
	if _, err := sess.AddDaemonWindow("shell", nil); err != nil {
		t.Fatalf("AddDaemonWindow failed: %v", err)
	}
	pushes := recordStateSink(sess)

	const n = 20
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = sess.SwitchDaemonWorkspace(i%maxDaemonWorkspaces + 1)
		}(i)
	}
	wg.Wait()

	got := pushes()
	if len(got) == 0 {
		t.Fatal("no pushes recorded")
	}
	for i := 1; i < len(got); i++ {
		if got[i].Version <= got[i-1].Version {
			t.Fatalf("push %d version %d arrived after %d", i, got[i].Version, got[i-1].Version)
		}
	}
	if last := got[len(got)-1]; last.Version != sess.GetState().Version {
		t.Errorf("last push version = %d, want the final state version %d", last.Version, sess.GetState().Version)
	}
}
