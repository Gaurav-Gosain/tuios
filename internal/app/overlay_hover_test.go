package app

import (
	"testing"
)

// TestKillSessionGoNextFallsBackToQuit checks the fallback: with no next
// session, or a switch that cannot succeed, the kill row quits outright
// instead of leaving the client on a dead session.
func TestKillSessionGoNextFallsBackToQuit(t *testing.T) {
	m := NewOS(OSOptions{})
	m.Width, m.Height = 120, 40

	if cmd := m.KillSessionGoNext(""); cmd == nil {
		t.Error("no next session: expected the quit fallback command")
	}
	if !m.QuitRequested {
		t.Error("the fallback did not record the quit intent")
	}

	// A named next session outside daemon mode cannot be switched to, so it
	// also falls back to quitting.
	m2 := NewOS(OSOptions{})
	m2.Width, m2.Height = 120, 40
	if cmd := m2.KillSessionGoNext("other"); cmd == nil {
		t.Error("failed switch: expected the quit fallback command")
	}
}
