package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// TestClientDA1DecidesSixel: an SSH client's terminal is guessed from its
// TERM, and its DA1 answer replaces the guess both ways. A pinned
// TUIOS_SIXEL_GRAPHICS is not replaced. A local terminal is asked again only
// when it answered the startup probe late, and its answer is then taken. The
// question is XTVERSION before DA1, so a terminal that stores images in its
// cells is known before its DA1 could turn sixel on.
func TestClientDA1DecidesSixel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		client ClientKind
		guess  bool
		pinned bool
		late   bool
		da1    []int
		want   bool
		asks   bool
	}{
		{"guessed yes, answers no", ClientSSH, true, false, false, []int{62, 22}, false, true},
		{"guessed no, answers yes", ClientSSH, false, false, false, []int{62, 4, 22}, true, true},
		{"pinned", ClientSSH, true, true, false, []int{62, 22}, true, false},
		{"local", ClientLocal, true, false, false, []int{62, 22}, true, false},
		{"local, answered late", ClientLocal, false, false, true, []int{62, 4, 22}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caps := &HostCapabilities{SixelGraphics: tc.guess, SixelPinned: tc.pinned, DA1Late: tc.late}
			m := &OS{Client: tc.client, Caps: caps}
			m.SixelPassthrough = NewSixelPassthroughWithOptions(SixelPassthroughOptions{Caps: caps})
			probe := m.sixelProbe()
			if asks := probe != nil; asks != tc.asks {
				t.Errorf("probe sent = %v, want %v", asks, tc.asks)
			}
			if probe != nil {
				want := tea.RawMsg{Msg: xtversionQuery + ansi.RequestPrimaryDeviceAttributes}
				if got := probe(); got != want {
					t.Errorf("probe = %q, want XTVERSION then DA1 %q", got, want.Msg)
				}
			}
			if !m.handleSixelProbe(uv.PrimaryDeviceAttributesEvent(tc.da1)) {
				t.Fatal("the DA1 answer was not taken")
			}
			if got := m.SixelPassthrough.Advertised(); got != tc.want {
				t.Errorf("sixel shown = %v, want %v", got, tc.want)
			}
		})
	}
}
