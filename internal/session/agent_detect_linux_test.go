//go:build linux

package session

import (
	"testing"
)

// TestParseStatTPGID checks the tpgid is read from field 8 even when the comm in
// field 2 contains spaces and parentheses.
func TestParseStatTPGID(t *testing.T) {
	// pid (comm) state ppid pgrp session tty_nr tpgid ...
	line := "1234 (weird (name) x) S 1000 1234 1000 34816 4321 4194304 ..."
	got, ok := parseStatTPGID(line)
	if !ok || got != 4321 {
		t.Fatalf("parseStatTPGID = (%d, %v), want (4321, true)", got, ok)
	}

	if _, ok := parseStatTPGID("garbage without paren"); ok {
		t.Error("parseStatTPGID accepted a line with no ')'")
	}
	if _, ok := parseStatTPGID("1 (init) S 0 1"); ok {
		t.Error("parseStatTPGID accepted a truncated line")
	}
}
