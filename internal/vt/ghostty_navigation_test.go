//go:build ghostty

package vt

import "testing"

// A private OSC number reaches Callbacks.PrivateOSC on the ghostty backend
// too, with the whole body, after the lock is released.
func TestGhosttyPrivateOSCCallback(t *testing.T) {
	term := NewGhosttyTerminal(20, 5)
	defer term.Close()
	var got []string
	term.SetCallbacks(Callbacks{
		PrivateOSC: func(cmd int, data []byte) {
			if cmd == 7777 {
				got = append(got, string(data))
			}
		},
	})
	if _, err := term.Write([]byte("\x1b]7777;tuios-nvim-navigator;focus;right\x07")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(got) != 1 || got[0] != "7777;tuios-nvim-navigator;focus;right" {
		t.Fatalf("private OSC callbacks = %q, want [7777;tuios-nvim-navigator;focus;right]", got)
	}
}
