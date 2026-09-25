package shot

import (
	"testing"
)

// TestAutoControlsAreTheLights pins what a default capture draws in its title
// bar. auto used to resolve to three accent-tinted dots at the traffic lights'
// size, spacing and position, which on a blue theme read as the lights drawn
// wrong rather than as a quieter option.
//
// Negative control: putting the ControlsDots arm back and making "auto" select
// it made every one of these three pixels the theme accent and failed.
func TestAutoControlsAreTheLights(t *testing.T) {
	in := FrameInputs{Palette: XTermPalette(), Accents: []Color{RGB(0x00, 0x00, 0xff)}}
	for _, spelling := range []string{"", "auto", "macos", "dots"} {
		f := BuildFrame(FrameSpec{Frame: "window", Controls: spelling, Scale: 1}, in)
		if f.Controls != ControlsMacOS {
			t.Errorf("controls %q resolved to %v, want the macOS lights", spelling, f.Controls)
		}
	}
	for spelling, want := range map[string]ControlsStyle{
		"glyphs": ControlsGlyphs,
		"none":   ControlsNone,
	} {
		f := BuildFrame(FrameSpec{Frame: "window", Controls: spelling, Scale: 1}, in)
		if f.Controls != want {
			t.Errorf("controls %q resolved to %v, want %v", spelling, f.Controls, want)
		}
	}
}
