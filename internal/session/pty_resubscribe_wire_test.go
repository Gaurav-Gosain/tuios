package session

import (
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
