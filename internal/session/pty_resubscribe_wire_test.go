package session

import (
	"sync"
	"testing"
	"time"
)

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
