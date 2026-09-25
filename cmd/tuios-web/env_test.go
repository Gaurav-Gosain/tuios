package main

import (
	"os"
	"testing"
)

// TestGuestTerminalEnvIsXterm256 holds ephemeral web panes to the TERM daemon
// panes get. The server used to pin xterm-kitty a few lines after this, which
// needs a terminfo entry the server may not have, and TERM_PROGRAM=tuios-web,
// which every spawn path overwrote anyway.
func TestGuestTerminalEnvIsXterm256(t *testing.T) {
	t.Setenv("TERM", "dumb")
	t.Setenv("COLORTERM", "")
	t.Setenv("TERM_PROGRAM", "before")

	pinGuestTerminalEnv()

	if got := os.Getenv("TERM"); got != "xterm-256color" {
		t.Errorf("TERM = %q, want xterm-256color", got)
	}
	if got := os.Getenv("COLORTERM"); got != "truecolor" {
		t.Errorf("COLORTERM = %q, want truecolor, or getTerminalEnv stops trusting TERM", got)
	}
	if got := os.Getenv("TERM_PROGRAM"); got != "before" {
		t.Errorf("TERM_PROGRAM = %q; the spawn paths own it and the server should leave it alone", got)
	}
}
