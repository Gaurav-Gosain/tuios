//go:build linux

package procinfo

import "testing"

// TestStatField checks the tpgid is read from field 8 even when the comm in
// field 2 contains spaces and parentheses.
func TestStatField(t *testing.T) {
	// pid (comm) state ppid pgrp session tty_nr tpgid ...
	line := "1234 (weird (name) x) S 1000 1234 1000 34816 4321 4194304 ..."
	got, ok := StatField(line, 8)
	if !ok || got != 4321 {
		t.Fatalf("StatField = (%d, %v), want (4321, true)", got, ok)
	}

	if _, ok := StatField("garbage without paren", 8); ok {
		t.Error("StatField accepted a line with no ')'")
	}
	if _, ok := StatField("1 (init) S 0 1", 8); ok {
		t.Error("StatField accepted a truncated line")
	}
}
