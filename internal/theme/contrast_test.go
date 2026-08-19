package theme

import (
	"image/color"
	"math"
	"testing"

	"github.com/charmbracelet/x/exp/charmtone"
)

// TestContrastRatioIsWCAG pins the two ends of the scale and the middle the
// chrome actually lives at. Every colour decision in the dock is argued with
// these numbers, so the numbers themselves have to be the standard's.
func TestContrastRatioIsWCAG(t *testing.T) {
	black, white := color.RGBA{A: 0xFF}, color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	for _, tc := range []struct {
		name string
		a, b color.Color
		want float64
	}{
		{"black on white", black, white, 21},
		{"white on black", white, black, 21},
		{"a colour against itself", charmtone.BBQ, charmtone.BBQ, 1},
		{"the dim step on the panel", charmtone.Smoke, charmtone.BBQ, 7.37},
		{"the muted step on the panel", charmtone.Squid, charmtone.BBQ, 3.71},
	} {
		if got := ContrastRatio(tc.a, tc.b); math.Abs(got-tc.want) > 0.01 {
			t.Errorf("%s measures %.2f:1, want %.2f:1", tc.name, got, tc.want)
		}
	}
}

// TestReadableClearsTheFloorForAnyAccent is the reason Readable exists. The
// accent follows the terminal theme, so it is the one chrome colour nobody has
// measured: a near-black brand blue on the panel is 1.06:1, which is a label
// that is technically drawn and practically absent.
func TestReadableClearsTheFloorForAnyAccent(t *testing.T) {
	grounds := map[string]color.Color{"panel": charmtone.BBQ, "canvas": charmtone.Pepper, "surface": charmtone.Char}
	accents := map[string]color.Color{
		"charple":     charmtone.Charple,
		"near black":  color.RGBA{R: 0x10, G: 0x10, B: 0x14, A: 0xFF},
		"dark indigo": color.RGBA{R: 0x24, G: 0x17, B: 0x73, A: 0xFF},
		"deep red":    color.RGBA{R: 0x5A, G: 0x00, B: 0x00, A: 0xFF},
		"mid green":   color.RGBA{R: 0x2E, G: 0x7D, B: 0x32, A: 0xFF},
	}
	for gn, ground := range grounds {
		for an, accent := range accents {
			got := ContrastRatio(Readable(accent, ground), ground)
			if got < ContrastFloor {
				t.Errorf("%s on %s lifts to %.2f:1, under the %.1f:1 floor", an, gn, got, ContrastFloor)
			}
		}
	}
}

// TestReadableLeavesALegibleColourAlone: a colour that already clears the floor
// is returned untouched, so Readable cannot wash out a palette that was chosen
// deliberately.
func TestReadableLeavesALegibleColourAlone(t *testing.T) {
	for _, c := range []color.Color{charmtone.Butter, charmtone.Smoke, charmtone.Salt} {
		if got := Readable(c, charmtone.BBQ); got != c {
			t.Errorf("%v already clears the floor on the panel but Readable returned %v", c, got)
		}
	}
}

// TestReadableSpendsTheLeastLuminanceItCan: lifting to the floor and stopping
// is what keeps the accent's hue on the pill. Blending all the way to the text
// colour would be legible and would also make every state look the same.
func TestReadableSpendsTheLeastLuminanceItCan(t *testing.T) {
	lifted := Readable(charmtone.Charple, charmtone.BBQ)
	if got := ContrastRatio(lifted, charmtone.BBQ); got > ContrastFloor+1 {
		t.Errorf("the accent was lifted to %.2f:1, well past the %.1f:1 it needed", got, ContrastFloor)
	}
	if lifted == ContrastText(charmtone.BBQ) {
		t.Error("the accent was blended all the way to the text colour, losing its hue")
	}
}

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

// A window's border is drawn on the pane it frames, and both come from the same
// theme, so nothing guarantees they differ until something measures them.
func TestBordersStayVisibleOnTheirPane(t *testing.T) {
	for _, name := range []string{"", "catppuccin_mocha", "builtin_solarized_light", "unikitty"} {
		_ = Initialize(name)
		bg := TerminalBg()
		for _, b := range []struct {
			what string
			ink  color.Color
		}{
			{"unfocused", BorderUnfocused()},
			{"window-mode focused", BorderFocusedWindow()},
			{"terminal-mode focused", BorderFocusedTerminal()},
		} {
			if got := ContrastRatio(b.ink, bg); got < MarkFloor {
				t.Errorf("theme %q: the %s border measures %.2f:1 on its pane, want at least %.1f:1",
					name, b.what, got, MarkFloor)
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
