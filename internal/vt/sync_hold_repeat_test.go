//go:build !ghostty

package vt

import (
	"testing"
	"time"
)

// A guest that repeats 2026h without ever closing the update is honored for
// syncMaxHold from the first open, not from the latest repeat.
func TestRepeatedSyncOpenDoesNotExtendTheHold(t *testing.T) {
	e := NewEmulator(10, 3)
	defer func() { _ = e.Close() }()
	_, _ = e.Write([]byte("\x1b[?2026h"))
	_, serial := e.SyncUpdate()
	// Pretend the update opened longer ago than the limit.
	e.syncSetAtNanos.Store(time.Now().Add(-2 * syncMaxHold).UnixNano())
	_, _ = e.Write([]byte("\x1b[?2026h"))
	if open, again := e.SyncUpdate(); open || again != serial {
		t.Fatalf("after a repeated 2026h the update reads open=%v serial=%d (was %d); want it expired, same serial", open, again, serial)
	}
}
