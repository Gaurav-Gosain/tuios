package app

import (
	"testing"
)

// The dock engine's guards. The first one is the invariant the whole component
// design was shaped around and is the reason the engine parks on a channel
// receive instead of holding a ticker.

// TestDockSanitizeKeepsColourAndDropsControls pins the launder rules, which are
// half the contract a component author is writing against.
func TestDockSanitizeKeepsColourAndDropsControls(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"\x1b[31mred\x1b[0m", "\x1b[31mred\x1b[0m"},
		{"\x1b[1;32mbold green\x1b[m", "\x1b[1;32mbold green\x1b[m"},
		{"before\x1b[2Jafter", "beforeafter"},
		{"move\x1b[10;20Hhere", "movehere"},
		{"bell\aand\bback", "bellandback"},
		{"tab\tseparated", "tabseparated"},
		{"\x1b]0;title\x07plain", "plain"},
		{"\x1b]2;a\x1b\\tail", "tail"},
	}
	for _, tc := range cases {
		if got := dockSanitize(tc.in); got != tc.want {
			t.Errorf("dockSanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
