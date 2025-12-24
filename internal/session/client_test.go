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

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = client.Close()
		}()
	}

	wg.Wait()

	// Verify done channel is closed
	select {
	case <-client.done:
		// Good - channel is closed
	default:
		t.Error("done channel should be closed after Close()")
	}

	// Calling Close() again should be safe
	if err := client.Close(); err != nil {
		t.Errorf("Close() returned error on re-close: %v", err)
	}
}

// TestClientCloseIdempotent verifies Close() can be called multiple times safely.
func TestClientCloseIdempotent(t *testing.T) {
	cfg := &ClientConfig{
		Version: "test",
	}
	client := NewClient(cfg)

	// First close
	if err := client.Close(); err != nil {
		t.Errorf("first Close() failed: %v", err)
	}

	// Second close should not panic
	if err := client.Close(); err != nil {
		t.Errorf("second Close() failed: %v", err)
	}

	// Third close should still be safe
	if err := client.Close(); err != nil {
		t.Errorf("third Close() failed: %v", err)
	}
}
