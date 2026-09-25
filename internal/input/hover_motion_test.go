package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestGuestMotionForwardingFollowsGuestMouseMode guards what the host's
// all-motion tracking must not leak: a guest sees only the motion its own mode
// would have reported.
func TestGuestMotionForwardingFollowsGuestMouseMode(t *testing.T) {
	cases := []struct {
		name   string
		enable string
		button tea.MouseButton
		want   bool
	}{
		{"any-event, button free", "\x1b[?1003h", tea.MouseNone, true},
		{"any-event, button held", "\x1b[?1003h", tea.MouseLeft, true},
		{"button-event, button free", "\x1b[?1002h", tea.MouseNone, false},
		{"button-event, button held", "\x1b[?1002h", tea.MouseLeft, true},
		{"normal tracking, button free", "\x1b[?1000h", tea.MouseNone, false},
		{"normal tracking, button held", "\x1b[?1000h", tea.MouseLeft, false},
		{"no mouse mode", "", tea.MouseLeft, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			em := vt.NewEmulator(38, 18)
			t.Cleanup(func() { _ = em.Close() })
			if tc.enable != "" {
				if _, err := em.Write([]byte(tc.enable)); err != nil {
					t.Fatalf("enable mouse tracking: %v", err)
				}
			}
			if got := guestWantsMotion(em, tc.button); got != tc.want {
				t.Errorf("forward = %v, want %v", got, tc.want)
			}
		})
	}
}
