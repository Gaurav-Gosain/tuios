package session

import (
	"sync"
	"testing"
	"time"
)

// TestBroadcastsReachAPeerInTheOrderTheyWereMade pins the daemon's delivery
// order. A client syncs after every change it makes, and each sync is
// forwarded to every peer as a whole snapshot. The snapshots are last-value-
// wins, so a peer that takes two of them in the wrong order ends on the state
// the pusher had already left: the tree it turned tiling on with, not the one
// it rotated a moment later.
//
// NEGATIVE CONTROL: with broadcastToSession starting a goroutine per send and
// nothing ordering the goroutines, this fails with a later push arriving
// before an earlier one, usually within the first few hundred pushes.
func TestBroadcastsReachAPeerInTheOrderTheyWereMade(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "ordered")
	pusher := attachTestClient(t, "ordered")
	watcher := attachTestClient(t, "ordered")

	const pushes = 1000
	var mu sync.Mutex
	var seen []float64
	last := make(chan struct{}, 1)
	watcher.OnStateSync(func(state *SessionState, _, _ string) {
		// A peer that applies each snapshot takes longer over one than the
		// daemon takes to forward the next, which is what lets the sends to it
		// pile up behind its socket. The pile is the condition under test: a
		// peer that keeps up never gives two sends the chance to pass.
		time.Sleep(time.Millisecond)
		mu.Lock()
		seen = append(seen, state.MasterRatio)
		n := len(seen)
		mu.Unlock()
		if state.MasterRatio == float64(pushes) && n > 0 {
			select {
			case last <- struct{}{}:
			default:
			}
		}
	})

	// Each push moves one field the fingerprint covers, so every one of them
	// is forwarded, and the field counts up so the order is legible.
	base := sess.GetState()
	for i := 1; i <= pushes; i++ {
		st := *base
		st.MasterRatio = float64(i)
		if err := pusher.UpdateState(&st); err != nil {
			t.Fatalf("push %d: %v", i, err)
		}
	}

	select {
	case <-last:
	case <-time.After(30 * time.Second):
		mu.Lock()
		n := len(seen)
		mu.Unlock()
		t.Fatalf("the peer never saw the last push: %d of %d arrived", n, pushes)
	}

	mu.Lock()
	defer mu.Unlock()
	for i := 1; i < len(seen); i++ {
		if seen[i] < seen[i-1] {
			t.Fatalf("push %.0f reached the peer after push %.0f; broadcasts to one client are written in the order they were made", seen[i], seen[i-1])
		}
	}
	if len(seen) != pushes {
		t.Fatalf("the peer saw %d syncs for %d pushes", len(seen), pushes)
	}
}
