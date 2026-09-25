package session

import (
	"sync"
	"testing"
)

// TestClientIDsAreUnique: connections accepted in the same clock tick get
// different ids. They were the time in nanoseconds alone, which on macOS moves
// in microseconds, so two clients could share an id and one replaced the other
// in the client table.
func TestClientIDsAreUnique(t *testing.T) {
	const n = 20000
	ids := make([]string, n)
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Go(func() {
			for i := w; i < n; i += 4 {
				ids[i] = newClientID()
			}
		})
	}
	wg.Wait()
	seen := make(map[string]bool, n)
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("client id %s was handed out twice", id)
		}
		seen[id] = true
	}
}
