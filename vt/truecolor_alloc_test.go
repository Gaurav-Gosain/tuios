package vt

import (
	"fmt"
	"image/color"
	"strings"
	"testing"
)

// TestTruecolorGradientAllocatesPerSlab pins the cost of a gradient, where
// every cell sets a truecolor foreground no other cell shares (lolcat, a
// truecolor prompt, a heat map). Boxing each colour into a color.Color was an
// allocation per cell; the slab makes it one per rgbSlabLen colours.
func TestTruecolorGradientAllocatesPerSlab(t *testing.T) {
	e := NewEmulator(80, 4)
	var b strings.Builder
	frame := 0
	line := func() string {
		b.Reset()
		b.WriteString("\x1b[H")
		for i := range 64 {
			fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dmx", i*4, frame%256, (frame/256)%256)
		}
		frame++
		return b.String()
	}
	lines := make([]string, 300)
	for i := range lines {
		lines[i] = line()
	}
	n := 0
	got := testing.AllocsPerRun(len(lines)-1, func() {
		e.WriteString(lines[n])
		n++
	})
	// 64 new colours a line is one slab; allow one more for the slab
	// boundary falling inside the line.
	if got > 2 {
		t.Errorf("a line of 64 new truecolor cells allocates %.1f times, want at most 2", got)
	}
}

// TestBoxedRGBIsTheConvertedValue checks that a colour boxed into the slab is
// indistinguishable from the plain conversion: the same dynamic type, equal
// under ==, and the same components.
func TestBoxedRGBIsTheConvertedValue(t *testing.T) {
	e := NewEmulator(10, 2)
	for i := range 3 * rgbSlabLen {
		want := color.RGBA{R: uint8(i), G: uint8(i * 3), B: uint8(i * 7), A: 0xff}
		got := e.boxRGB(want)
		if c, ok := got.(color.RGBA); !ok || c != want {
			t.Fatalf("boxed %v reads back as %#v", want, got)
		}
		if got != color.Color(want) {
			t.Fatalf("boxed %v is not == to the converted value", want)
		}
		r, g, b, a := got.RGBA()
		wr, wg, wb, wa := want.RGBA()
		if r != wr || g != wg || b != wb || a != wa {
			t.Fatalf("boxed %v RGBA() differs", want)
		}
	}
}
