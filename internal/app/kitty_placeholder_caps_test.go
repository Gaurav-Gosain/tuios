package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
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

func TestParseHostIdentityHandlesBothSpellings(t *testing.T) {
	for _, tc := range []struct {
		in      string
		name    string
		version [3]int
	}{
		{"\x1bP>|ghostty 1.3.1\x1b\\", "ghostty", [3]int{1, 3, 1}},
		{"\x1bP>|kitty(0.32.2)\x1b\\", "kitty", [3]int{0, 32, 2}},
		{"\x1bP>|foot(1.16.2)\x1b\\", "foot", [3]int{1, 16, 2}},
	} {
		name, version, ok := parseHostIdentity(tc.in)
		if !ok || name != tc.name || version != tc.version {
			t.Errorf("parseHostIdentity(%q) = %q %v (ok=%v), want %q %v",
				tc.in, name, version, ok, tc.name, tc.version)
		}
	}
}

// TestTheSettingOverridesTheDetection is the escape hatch. The table will be
// wrong about some terminal eventually, and the user has to be able to say so.
func TestTheSettingOverridesTheDetection(t *testing.T) {
	m := &OS{Caps: &HostCapabilities{KittyGraphics: true, KittyPlaceholders: false}}
	m.KittyPassthrough = newTestKittyPassthrough(t)

	m.Settings.KittyPlaceholders = config.KittyPlaceholdersOn
	if !m.placeholdersEnabled() {
		t.Error(`"on" did not turn placeholders on over a host the table does not know`)
	}

	m.Caps.KittyPlaceholders = true
	m.Settings.KittyPlaceholders = config.KittyPlaceholdersOff
	if m.placeholdersEnabled() {
		t.Error(`"off" did not turn placeholders off on a host that supports them`)
	}

	m.Settings.KittyPlaceholders = config.KittyPlaceholdersAuto
	if !m.placeholdersEnabled() {
		t.Error(`"auto" did not follow the host`)
	}
	m.Caps.KittyPlaceholders = false
	if m.placeholdersEnabled() {
		t.Error(`"auto" drew placeholders on a host that cannot`)
	}
}
