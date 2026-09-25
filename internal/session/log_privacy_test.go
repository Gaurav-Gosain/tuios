package session

import (
	"strings"
	"testing"
)

// TestRaisingToVerboseWarns checks the notice the level boundary owes the
// caller: raising the level starts recording content, and the log says so in
// the same place the caller will read the capture back from.
func TestRaisingToVerboseWarns(t *testing.T) {
	restoreLevel(t, DebugOff)
	ClearLogBuffer()

	// The buffer is package state and other tests in this package log into it
	// while this one runs, so the warning is not reliably the last entry. Read
	// the length first and look only at what this call appended: that still
	// fails if raising the level logs no warning, which is the property here.
	before := len(GetLogEntries(0))

	SetDebugLevel(DebugVerbose)

	entries := GetLogEntries(0)
	if len(entries) <= before {
		t.Fatal("raising the level to verbose logged nothing")
	}
	warned := false
	for _, e := range entries[before:] {
		if strings.Contains(e.Message, "records pane content, window titles and paths") {
			warned = true
			break
		}
	}
	if !warned {
		got := make([]string, 0, len(entries)-before)
		for _, e := range entries[before:] {
			got = append(got, e.Message)
		}
		t.Fatalf("no content warning on raising the level, got %q", got)
	}

	// Lowering must not warn, and neither must a level at or below messages.
	ClearLogBuffer()
	SetDebugLevel(DebugMessages)
	for _, e := range GetLogEntries(0) {
		if strings.Contains(e.Message, "records pane content") {
			t.Fatalf("lowering the level warned: %q", e.Message)
		}
	}
}
