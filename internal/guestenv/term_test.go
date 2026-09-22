package guestenv

import (
	"testing"

	"github.com/charmbracelet/colorprofile"
)

// TestProfileToEnv pins the TERM mapping. The CLI client's hello used to have
// its own copy, which kept a parent TERM without 256color for a 256 color
// profile and sent "dumb" for an unknown one, so a daemon pane was told
// something different from a standalone pane under the same terminal.
func TestProfileToEnv(t *testing.T) {
	tests := []struct {
		name      string
		profile   colorprofile.Profile
		parent    string
		wantTerm  string
		wantColor string
	}{
		{name: "truecolor keeps parent", profile: colorprofile.TrueColor, parent: "xterm-kitty", wantTerm: "xterm-kitty", wantColor: "truecolor"},
		{name: "truecolor without parent", profile: colorprofile.TrueColor, wantTerm: "xterm-256color", wantColor: "truecolor"},
		{name: "256 keeps a 256color parent", profile: colorprofile.ANSI256, parent: "foot-256color", wantTerm: "foot-256color"},
		{name: "256 under screen", profile: colorprofile.ANSI256, parent: "screen", wantTerm: "screen-256color"},
		{name: "256 under tmux", profile: colorprofile.ANSI256, parent: "tmux", wantTerm: "tmux-256color"},
		{name: "256 under plain xterm", profile: colorprofile.ANSI256, parent: "xterm", wantTerm: "xterm-256color"},
		{name: "ansi keeps parent", profile: colorprofile.ANSI, parent: "vt100", wantTerm: "vt100"},
		{name: "ansi under dumb", profile: colorprofile.ANSI, parent: "dumb", wantTerm: "xterm"},
		{name: "ascii", profile: colorprofile.Ascii, parent: "xterm", wantTerm: "dumb"},
		{name: "notty", profile: colorprofile.NoTTY, wantTerm: "dumb"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			term, color := ProfileToEnv(tc.profile, tc.parent)
			if term != tc.wantTerm || color != tc.wantColor {
				t.Errorf("ProfileToEnv(%v, %q) = (%q, %q), want (%q, %q)", tc.profile, tc.parent, term, color, tc.wantTerm, tc.wantColor)
			}
		})
	}
}
