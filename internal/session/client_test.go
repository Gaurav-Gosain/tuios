package session

import (
	"sync"
	"testing"
)

// TestClientCloseConcurrent verifies that concurrent Close() calls are safe.
func TestClientCloseConcurrent(t *testing.T) {
	cfg := &ClientConfig{
		Version: "test",
	}
	client := NewClient(cfg)

	var wg sync.WaitGroup
	const numGoroutines = 100

	for range numGoroutines {
		wg.Go(func() {
			_ = client.Close()
		})
	}

	wg.Wait()

	select {
	case <-client.done:
	default:
		t.Error("done channel should be closed after Close()")
	}

	if err := client.Close(); err != nil {
		t.Errorf("Close() returned error on re-close: %v", err)
	}
}
