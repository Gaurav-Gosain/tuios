package session

import (
	"bytes"
	"testing"
)

// TestNestProbeSafe pins which terminals get no probe: the ones that do not
// parse OSC and would print it.
func TestNestProbeSafe(t *testing.T) {
	for term, want := range map[string]bool{
		"xterm-256color": true, "xterm-kitty": true, "xterm-ghostty": true, "alacritty": true,
		"wezterm": true, "tmux-256color": true, "screen-256color": true, "contour": true,
		"linux": false, "linux-16color": false, "dumb": false, "vt100": false, "cons25": false, "": false,
	} {
		if got := NestProbeSafe(term); got != want {
			t.Errorf("NestProbeSafe(%q) = %v, want %v", term, got, want)
		}
	}
}

// TestNestProbeSplitAcrossReads covers a probe that arrives in two reads.
func TestNestProbeSplitAcrossReads(t *testing.T) {
	nonce, seq := NewNestProbe()
	p := &PTY{sessionID: "split-session"}
	cut := len(seq) / 2
	p.scanNestProbes(append([]byte("before"), seq[:cut]...))
	p.scanNestProbes(append(bytes.Clone(seq[cut:]), "after"...))
	if got := seenNestProbe(nonce); got != "split-session" {
		t.Errorf("a probe split across two reads was recorded against %q, want split-session", got)
	}
}
