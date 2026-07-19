package terminal

import "testing"

// A locally spawned shell must advertise the graphics protocols tuios can
// forward to the host terminal. Hardcoding TERM_PROGRAM=TUIOS meant image
// tools inside a window fell back to block art even when tuios was passing
// kitty graphics straight through to a capable terminal.
func TestGuestTermProgramFollowsGraphicsCapabilities(t *testing.T) {
	t.Cleanup(func() { SetGraphicsCapabilities(false, false) })

	tests := []struct {
		name  string
		kitty bool
		sixel bool
		want  string
	}{
		{name: "no passthrough", want: "TUIOS"},
		{name: "kitty passthrough", kitty: true, want: "ghostty"},
		{name: "sixel passthrough", sixel: true, want: "WezTerm"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			SetGraphicsCapabilities(tc.kitty, tc.sixel)
			if got := guestTermProgram(); got != tc.want {
				t.Errorf("guestTermProgram() = %q, want %q", got, tc.want)
			}
		})
	}
}
