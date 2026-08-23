package terminal

import "testing"

// A locally spawned shell must advertise the graphics protocols tuios can
// forward to the host terminal. Hardcoding TERM_PROGRAM=TUIOS meant image
// tools inside a window fell back to block art even when tuios was passing
// kitty graphics straight through to a capable terminal.
func TestGuestTermProgramFollowsGraphicsCapabilities(t *testing.T) {
	t.Cleanup(func() { SetGraphicsCapabilities(false, false, false) })

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
			SetGraphicsCapabilities(tc.kitty, tc.sixel, false)
			if got := guestTermProgram(); got != tc.want {
				t.Errorf("guestTermProgram() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestGuestKittyAnimation pins what a pane is told about frame edits.
//
// A guest cannot work this out for itself: tuios does not relay the host's
// answer to an a=f back into the pane, and TERM and KITTY_WINDOW_ID are
// inherited straight through, so they name the host terminal rather than the
// pane. wlterm reads this variable to decide whether its cheap transport is
// safe, so a wrong "1" here is a frozen picture in somebody's pane.
func TestGuestKittyAnimation(t *testing.T) {
	t.Cleanup(func() { SetGraphicsCapabilities(false, false, false) })
	for _, tc := range []struct {
		name      string
		kitty     bool
		animation bool
		want      string
	}{
		{name: "nothing forwarded", want: "TUIOS_KITTY_ANIMATION=0"},
		{name: "graphics but no frame edits", kitty: true, want: "TUIOS_KITTY_ANIMATION=0"},
		{name: "frame edits without graphics", animation: true, want: "TUIOS_KITTY_ANIMATION=0"},
		{name: "both", kitty: true, animation: true, want: "TUIOS_KITTY_ANIMATION=1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			SetGraphicsCapabilities(tc.kitty, false, tc.animation)
			if got := guestKittyAnimation(); got != tc.want {
				t.Errorf("guestKittyAnimation() = %q, want %q", got, tc.want)
			}
		})
	}
}
