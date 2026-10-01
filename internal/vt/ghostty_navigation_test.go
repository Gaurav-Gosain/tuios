//go:build ghostty

package vt

import "testing"

func TestGhosttyOSC7777NavigationCallback(t *testing.T) {
	term := NewGhosttyTerminal(20, 5)
	defer term.Close()
	var got []string
	term.SetCallbacks(Callbacks{
		TuiosNavigation: func(direction string) { got = append(got, direction) },
	})
	if _, err := term.Write([]byte("\x1b]7777;tuios-nvim-navigator;focus;right\x07")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(got) != 1 || got[0] != "right" {
		t.Fatalf("navigation callbacks = %q, want [right]", got)
	}
}
