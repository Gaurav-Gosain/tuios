package shot

import (
	"image"
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

// pixelName names a pixel when it is one of the three control colours, exactly.
func pixelName(img *image.RGBA, x, y int) string {
	c := img.RGBAAt(x, y)
	switch {
	case c.R == 0xff && c.G == 0x5f && c.B == 0x57:
		return "red"
	case c.R == 0xfe && c.G == 0xbc && c.B == 0x2e:
		return "amber"
	case c.R == 0x28 && c.G == 0xc8 && c.B == 0x40:
		return "green"
	}
	return ""
}
