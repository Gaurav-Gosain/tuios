package session

import (
	"strings"
	"testing"
)

// TestPTYResizeIsLogged pins the line `tuios logs` shows for a pane size
// change. A shell repaints its prompt for exactly one thing the daemon does,
// and this is the record of it: a pane that gains blank lines when focus moves
// is explained by whether this line appears with each move.
func TestPTYResizeIsLogged(t *testing.T) {
	_, sess := newTestDaemonSession(t)
	pty, err := sess.CreatePTY("win-resize-log", 65, 38, func(string) {})
	if err != nil {
		t.Fatalf("CreatePTY failed: %v", err)
	}
	ClearLogBuffer()

	if err := pty.Resize(80, 20); err != nil {
		t.Fatalf("pty.Resize failed: %v", err)
	}
	if err := pty.Resize(80, 20); err != nil {
		t.Fatalf("pty.Resize failed: %v", err)
	}

	want := "PTY " + shortID(pty.ID) + " resized 65x38 -> 80x20"
	var hits int
	for _, e := range GetLogEntries(0) {
		if strings.Contains(e.Message, want) {
			hits++
		}
	}
	if hits != 1 {
		t.Fatalf("log has %d lines %q, want 1: a change is logged once and a repeat not at all", hits, want)
	}
}
