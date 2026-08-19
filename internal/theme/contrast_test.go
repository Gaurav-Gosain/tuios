package theme

import (
	"image/color"
	"testing"
)

// The chrome's quiet ink has to stay legible on every ground the chrome paints.
// It is one colour used on four, so it is measured on all four rather than on
// whichever one it was picked against.
func TestQuietInkClearsItsGrounds(t *testing.T) {
	_ = Initialize("")
	pal := UI()

	grounds := []struct {
		name string
		bg   color.Color
	}{
		{"canvas", pal.Canvas},
		{"panel", pal.Panel},
		{"surface", pal.Surface},
	}
	for _, g := range grounds {
		if got := ContrastRatio(pal.FgMute, g.bg); got < MarkFloor {
			t.Errorf("FgMute on %s measures %.2f:1, want at least %.1f:1", g.name, got, MarkFloor)
		}
	}
	// The card is the top of the ramp and quiet ink does not clear it unaided,
	// so the one surface that writes on a card lifts it through Readable.
	if got := ContrastRatio(Readable(pal.FgMute, pal.Card), pal.Card); got < ContrastFloor {
		t.Errorf("lifted FgMute on the card measures %.2f:1, want at least %.1f:1", got, ContrastFloor)
	}
	for _, g := range append(grounds, struct {
		name string
		bg   color.Color
	}{"card", pal.Card}) {
		if got := ContrastRatio(pal.FgDim, g.bg); got < ContrastFloor {
			t.Errorf("FgDim on %s measures %.2f:1, want at least %.1f:1", g.name, got, ContrastFloor)
		}
	}
}

// A notification is a piece of the dock, so its ground is the chrome's and its
// contrast does not move when the terminal theme does.
func TestNotificationInkHoldsAcrossThemes(t *testing.T) {
	for _, name := range []string{"", "catppuccin_mocha", "builtin_solarized_light"} {
		_ = Initialize(name)
		bg := NotificationBg()
		if got := ContrastRatio(NotificationFg(), bg); got < ContrastFloor {
			t.Errorf("theme %q: message text measures %.2f:1 on its block, want at least %.1f:1", name, got, ContrastFloor)
		}
		for _, sev := range []string{"error", "warning", "success", "info"} {
			ink := ReadableAt(NotificationSeverity(sev), bg, MarkFloor)
			if got := ContrastRatio(ink, bg); got < MarkFloor {
				t.Errorf("theme %q: %s mark measures %.2f:1 on its block, want at least %.1f:1", name, sev, got, MarkFloor)
			}
		}
	}
	_ = Initialize("")
}

// The dock hairline is drawn straight onto the user's terminal background,
// which tuios never paints, so it has to survive both ends of the range.
func TestDockHairlineSurvivesEitherGround(t *testing.T) {
	_ = Initialize("")
	for _, g := range []struct {
		name string
		bg   color.Color
	}{
		{"a black terminal", color.RGBA{A: 0xff}},
		{"a white terminal", color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}},
	} {
		if got := ContrastRatio(NotificationRule(), g.bg); got < MarkFloor {
			t.Errorf("the hairline on %s measures %.2f:1, want at least %.1f:1", g.name, got, MarkFloor)
		}
	}
}
