//go:build ghostty

package vt

import "testing"

// The ghostty backend calls Callbacks.PrivateOSC for the same sequences as
// the pure emulator. See privateOSCCases.
func TestGhosttyPrivateOSC(t *testing.T) {
	runPrivateOSCCases(t, func() Terminal { return NewGhosttyTerminal(20, 3) })
}

// OSC 7777 now reaches libghostty-vt, which does not know it. It must leave
// the screen and the cursor as they were.
func TestGhosttyIgnoresTheNavigatorOSC(t *testing.T) {
	term := NewGhosttyTerminal(20, 3)
	defer term.Close()
	if _, err := term.Write([]byte("ab\x1b]7777;tuios-nvim-navigator;focus;right\x07cd")); err != nil {
		t.Fatal(err)
	}
	if got := term.String(); got[:4] != "abcd" {
		t.Fatalf("screen %q, want it to start with abcd", got)
	}
	if p := term.CursorPosition(); p.X != 4 || p.Y != 0 {
		t.Fatalf("cursor %v, want (4,0)", p)
	}
}
