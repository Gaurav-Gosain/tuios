package vt

import "testing"

func TestEmulatorRoutesTuiosNavigation(t *testing.T) {
	var got []string
	var active bool
	em := NewEmulator(80, 24)
	em.SetCallbacks(Callbacks{TuiosNavigation: func(direction string) {
		got = append(got, direction)
	}, NvimNavigatorState: func(value bool) {
		active = value
	}})

	_, _ = em.Write([]byte("\x1b]7777;tuios-nvim-navigator;focus;left\x07"))
	_, _ = em.Write([]byte("\x1b]7777;tuios-nvim-navigator;state;active\x07"))
	_, _ = em.Write([]byte("\x1b]7777;tuios-nvim-navigator;focus;sideways\x07"))
	if len(got) != 1 || got[0] != "left" {
		t.Fatalf("navigation callbacks = %q, want [left]", got)
	}
	if !active {
		t.Fatal("navigator state was not routed")
	}
}
