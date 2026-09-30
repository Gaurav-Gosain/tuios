package app

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// TestSSHClientDA1DecidesSixel: an SSH client's terminal is guessed from its
// TERM, and its DA1 answer replaces the guess both ways. A pinned
// TUIOS_SIXEL_GRAPHICS is not replaced, and a local terminal is not asked.
func TestSSHClientDA1DecidesSixel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		client ClientKind
		guess  bool
		pinned bool
		da1    []int
		want   bool
		asks   bool
	}{
		{"guessed yes, answers no", ClientSSH, true, false, []int{62, 22}, false, true},
		{"guessed no, answers yes", ClientSSH, false, false, []int{62, 4, 22}, true, true},
		{"pinned", ClientSSH, true, true, []int{62, 22}, true, false},
		{"local", ClientLocal, true, false, []int{62, 22}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caps := &HostCapabilities{SixelGraphics: tc.guess, SixelPinned: tc.pinned}
			m := &OS{Client: tc.client, Caps: caps}
			m.SixelPassthrough = NewSixelPassthroughWithOptions(SixelPassthroughOptions{Caps: caps})
			if asks := m.sixelProbe() != nil; asks != tc.asks {
				t.Errorf("probe sent = %v, want %v", asks, tc.asks)
			}
			if !m.handleSixelProbe(uv.PrimaryDeviceAttributesEvent(tc.da1)) {
				t.Fatal("the DA1 answer was not taken")
			}
			if got := m.SixelPassthrough.IsEnabled(); got != tc.want {
				t.Errorf("sixel shown = %v, want %v", got, tc.want)
			}
		})
	}
}
