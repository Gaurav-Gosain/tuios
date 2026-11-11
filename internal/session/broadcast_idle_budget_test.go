package session

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// The most one daemon-side publish may cost on a session nobody is attached
// to, for the state of unwatchedSession. Encoding that state for a broadcast
// nobody receives is about 154 allocations and 137 KB a publish; what is left
// without it (the live facts, the walk of the clients) is a few allocations
// and well under a kilobyte. The figures sit far above that remainder, so
// load and the daemon's own goroutines do not trip them, and far below the
// encode, so it cannot come back unseen.
const (
	unwatchedPublishAllocs = 16
	unwatchedPublishBytes  = 8 << 10
)

// unwatchedSession is a session with 100 windows and 50 cards and no pane
// behind any of them: CreateSession starts no shell, and the windows are
// only state. Its state sink is the daemon's own.
func unwatchedSession(t *testing.T, d *Daemon, name string) *Session {
	t.Helper()
	sess, err := d.manager.CreateSession(name, &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess.stateMu.Lock()
	sess.state.Windows = benchState(100).Windows
	sess.state.Tasks = pushCards(50).Tasks
	sess.stateMu.Unlock()
	return sess
}

// TestBroadcastToAnUnwatchedSessionIsNotEncoded: every daemon-side change
// publishes the whole session to its clients, and the push was encoded before
// anything looked for a client to send it to, so a session nobody is attached
// to (agents working overnight, a headless host) paid a full encode of its
// state on every change and threw it away.
//
// "no client" publishes the snapshot straight through publishState with the
// daemon's own sink, the way TestLateSnapshotIsDropped drives it
// (`sess.publishState(&SessionState{Version: 7, CurrentWorkspace: 7, changeSeq: 7})`),
// each call one change above the last so none returns early, and holds the
// publish to the budget above. Allocations are read from runtime.MemStats, not
// testing.AllocsPerRun, because the budget is in bytes as well as
// allocations; MemStats also counts the daemon's background goroutines, so
// the lowest of 5 runs is the one compared.
//
// It could pass wrongly in three ways, and each has a row:
//   - The publish never reached the sink (no sink installed, or a change
//     count the session had already delivered), so nothing was encoded
//     because nothing was sent. "no client" checks that the session recorded
//     every publish as delivered.
//   - The push is skipped for a session that does have a client. "one
//     attached client" publishes a name only that publish carries and waits
//     for the client to be sent it.
//   - The look counts only attached clients, so a client still attaching is
//     taken for nobody: the broadcast is not encoded and the walk that marks
//     that client, so its attach handler sends the state it missed, never
//     runs. "one client attaching" makes a daemon-side change between the
//     attach reply's snapshot and the reply, with no other client, and waits
//     for the change to reach the client.
//
// Each row has its own session: "no client" numbers its snapshots by hand,
// which leaves its session dropping real changes until its own count catches
// up.
func TestBroadcastToAnUnwatchedSessionIsNotEncoded(t *testing.T) {
	d, _ := startTestDaemon(t)

	t.Run("no client", func(t *testing.T) {
		sess := unwatchedSession(t, d, "unwatched")
		snap := sess.GetState()

		const runs, publishes = 5, 50
		next := snap.changeSeq
		var bestAllocs, bestBytes uint64
		for r := range runs {
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for range publishes {
				next++
				snap.changeSeq = next
				sess.publishState(snap)
			}
			runtime.ReadMemStats(&after)
			allocs := (after.Mallocs - before.Mallocs) / publishes
			size := (after.TotalAlloc - before.TotalAlloc) / publishes
			t.Logf("run %d: %d allocations, %d bytes a publish", r+1, allocs, size)
			if r == 0 || allocs < bestAllocs {
				bestAllocs = allocs
			}
			if r == 0 || size < bestBytes {
				bestBytes = size
			}
		}

		sess.pushMu.Lock()
		delivered := sess.deliveredSeq
		sess.pushMu.Unlock()
		if delivered != next {
			t.Fatalf("the session recorded change %d as delivered, want %d: the publishes did not all reach the sink, so nothing was measured", delivered, next)
		}

		if bestAllocs > unwatchedPublishAllocs || bestBytes > unwatchedPublishBytes {
			t.Errorf("ASSERTION: a publish to a session nobody is attached to took %d allocations and %d bytes (lowest of %d runs of %d), want at most %d and %d: the state is still encoded for nobody",
				bestAllocs, bestBytes, runs, publishes, unwatchedPublishAllocs, unwatchedPublishBytes)
		}
	})

	t.Run("one attached client", func(t *testing.T) {
		sess := unwatchedSession(t, d, "watched")
		c := attachTestClient(t, "watched")
		seen := takeSyncs(c)

		const name = "published while attached"
		snap := sess.GetState()
		snap.DisplayName = name
		sess.stateMu.RLock()
		snap.changeSeq = sess.changeSeq + 1
		sess.stateMu.RUnlock()
		sess.publishState(snap)

		deadline := time.After(5 * time.Second)
		for {
			select {
			case got := <-seen.ch:
				if got.trigger == "update" && got.state != nil && got.state.DisplayName == name {
					return
				}
			case <-deadline:
				t.Fatalf("ASSERTION: a publish to a session with a client attached never reached the client: no \"update\" state sync named %q within 5s", name)
			}
		}
	})

	t.Run("one client attaching", func(t *testing.T) {
		sess := unwatchedSession(t, d, "attaching")
		const name = "changed while attaching"

		// The hook runs on the daemon's connection goroutine; hookDone orders
		// what it did before the checks and cleanups below.
		hookDone := make(chan struct{})
		var once sync.Once
		hook := func() {
			once.Do(func() {
				defer close(hookDone)
				if err := sess.SetDisplayName(name); err != nil {
					t.Errorf("SetDisplayName: %v", err)
				}
			})
		}
		attachSnapshotTaken.Store(&hook)
		t.Cleanup(func() { attachSnapshotTaken.Store(nil) })

		c := NewTUIClient()
		if err := c.Connect("test", 80, 24); err != nil {
			t.Fatalf("connect: %v", err)
		}
		t.Cleanup(func() { _ = c.Close() })
		seen := takeSyncs(c)
		reply, err := c.AttachSession("attaching", false, 80, 24)
		if err != nil {
			t.Fatalf("attach: %v", err)
		}
		attachSnapshotTaken.Store(nil)
		select {
		case <-hookDone:
		case <-time.After(5 * time.Second):
			t.Fatal("the attach never ran the hook between its snapshot and its reply")
		}
		if reply.DisplayName == name {
			t.Fatalf("the attach reply already names the session %q: the change landed before the snapshot, so this row proves nothing", name)
		}
		c.StartReadLoop()

		deadline := time.After(5 * time.Second)
		for {
			select {
			case got := <-seen.ch:
				if got.state != nil && got.state.DisplayName == name {
					return
				}
			case <-deadline:
				t.Fatalf("ASSERTION: a daemon-side change made while the only client was attaching never reached it: no state sync named %q within 5s", name)
			}
		}
	})
}
