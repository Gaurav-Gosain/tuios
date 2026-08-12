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
		{"the muted step on the panel", charmtone.Oyster, charmtone.BBQ, 2.19},
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
