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
			_ = sess.SwitchDaemonWorkspace(i%defaultWorkspaces + 1)
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
