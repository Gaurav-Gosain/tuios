package app

import (
	"testing"
)

// TestHostDrawsPlaceholdersReadsTheTerminalsOwnAnswer pins the detection. It is
// a heuristic, and the point of the test is that it is a heuristic over what
// the terminal said about itself rather than over an inherited TERM.
func TestHostDrawsPlaceholders(t *testing.T) {
	reply := func(s string) string { return "\x1bP>|" + s + "\x1b\\" }

	for _, tc := range []struct {
		name string
		resp string
		want bool
	}{
		// The two spellings seen in the wild.
		{"ghostty 1.3.1 draws them", reply("ghostty 1.3.1"), true},
		{"kitty 0.32.2 draws them", reply("kitty(0.32.2)"), true},
		{"wezterm draws them", reply("wezterm 20240203"), true},
		// Older than the version that added the feature.
		{"kitty 0.26 is too old", reply("kitty(0.26.5)"), false},
		{"ghostty 0.9 is too old", reply("ghostty 0.9.0"), false},
		// A terminal nobody has vouched for falls back rather than guessing.
		{"an unknown terminal is not assumed", reply("someterm 9.9.9"), false},
		{"no answer at all is not assumed", "", false},
		{"a malformed answer is not assumed", "\x1bP>|\x1b\\", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hostDrawsPlaceholders(tc.resp); got != tc.want {
				t.Errorf("hostDrawsPlaceholders(%q) = %v, want %v", tc.resp, got, tc.want)
			}
		})
	}
}
