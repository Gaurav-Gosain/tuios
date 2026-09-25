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

	// Call Close() from 100 goroutines concurrently
	var wg sync.WaitGroup
	const numGoroutines = 100

	for range numGoroutines {
		wg.Go(func() {
			_ = client.Close()
		})
	}

	wg.Wait()

	// Verify done channel is closed
	select {
	case <-client.done:
		// Good: channel is closed
	default:
		t.Error("done channel should be closed after Close()")
	}

	// Calling Close() again should be safe
	if err := client.Close(); err != nil {
		t.Errorf("Close() returned error on re-close: %v", err)
	}
}
