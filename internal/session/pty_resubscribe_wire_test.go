package session

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

// This covers the same contract as pty_resubscribe_test.go one level up, over
// the real wire: a client hiding and showing a pane goes through the daemon's
// subscribe and unsubscribe handlers, and those are what have to carry the
// stream position between the two.

// collector accumulates the raw PTY bytes a client is sent.
type collector struct {
	mu  sync.Mutex
	buf []byte
}

func (c *collector) add(data []byte) {
	c.mu.Lock()
	c.buf = append(c.buf, data...)
	c.mu.Unlock()
}

func (c *collector) take() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.buf
	c.buf = nil
	return out
}

// waitFor polls until cond holds, so the test does not depend on how fast the
// daemon's streaming goroutine gets going.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestResubscribeOverTheWireReplaysOnlyWhatWasMissed(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "switching")

	ptyID := sess.ListPTYIDs()[0]
	client := attachTestClient(t, "switching")

	var got collector
	if err := client.SubscribePTY(ptyID, got.add); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Output stands in for fish's banner: whatever the pane printed, the client
	// has now seen it. Injected rather than driven through a shell so the test
	// turns on the subscription contract and not on a prompt's timing.
	marker := []byte("PANEMARK-7431\n")
	pty := sess.GetPTY(ptyID)
	pty.appendAndBroadcast(marker)
	waitFor(t, "the pane to print its marker", func() bool {
		got.mu.Lock()
		defer got.mu.Unlock()
		return bytes.Count(got.buf, marker) > 0
	})
	got.take()

	// Hide the pane and show it again, as a workspace switch does.
	for range 3 {
		client.UnsubscribePTY(ptyID)
		waitFor(t, "the daemon to drop the subscription", func() bool {
			pty.subscribersMu.RLock()
			defer pty.subscribersMu.RUnlock()
			return len(pty.subscribers) == 0
		})
		if err := client.SubscribePTY(ptyID, got.add); err != nil {
			t.Fatalf("resubscribe: %v", err)
		}
		waitFor(t, "the daemon to take the subscription", func() bool {
			pty.subscribersMu.RLock()
			defer pty.subscribersMu.RUnlock()
			return len(pty.subscribers) == 1
		})
	}

	// Give any replay time to arrive before concluding none did.
	time.Sleep(300 * time.Millisecond)
	if replayed := got.take(); len(replayed) != 0 {
		t.Errorf("three hide/show cycles replayed %q, want nothing", replayed)
	}
}
