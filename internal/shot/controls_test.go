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

// TestDefaultCaptureDrawsRedAmberGreen is the on-screen half of the same claim:
// the three discs in a rendered title bar really are the three colours, read
// back out of the pixels.
//
// Negative control: as above; the dots version painted all three from the blue
// accent and every channel comparison failed.
func TestDefaultCaptureDrawsRedAmberGreen(t *testing.T) {
	g := NewGrid(20, 3, RGB(0xcd, 0xd6, 0xf4), RGB(0x1e, 0x1e, 0x2e))
	f := BuildFrame(
		FrameSpec{Frame: "window", Controls: "auto", Padding: 10, Radius: 4, Scale: 1, Title: "t"},
		FrameInputs{Palette: XTermPalette(), Accents: []Color{RGB(0x00, 0x00, 0xff)}},
	)
	img, err := Raster(g, f)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			switch pixelName(img, x, y) {
			case "red":
				found["red"] = true
			case "amber":
				found["amber"] = true
			case "green":
				found["green"] = true
			}
		}
	}
	for _, want := range []string{"red", "amber", "green"} {
		if !found[want] {
			t.Errorf("a default capture drew no %s window control", want)
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
