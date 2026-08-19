package session

import (
	"image/color"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestColorToWireWrappedNil pins the daemon-side twin of the client's
// wrapped-nil color panic: a color.Color interface holding (*color.RGBA)(nil)
// slips past the type switch's case nil, and RGBA's value receiver then
// dereferences the nil pointer. Without the guard this test panics instead of
// failing.
func TestColorToWireWrappedNil(t *testing.T) {
	if got := colorToWire((*color.RGBA)(nil)); got != "" {
		t.Errorf("colorToWire(wrapped nil) = %q, want empty", got)
	}
}

// TestColorToWireStillEncodesRealColors keeps the guard from over-matching:
// every kind of color the wire format knows still round-trips to its encoding.
func TestColorToWireStillEncodesRealColors(t *testing.T) {
	cases := []struct {
		name string
		c    color.Color
		want string
	}{
		{"untyped nil", nil, ""},
		{"basic palette entry", ansi.BasicColor(1), "a1"},
		{"indexed palette entry", ansi.IndexedColor(200), "i200"},
		{"truecolor value", color.RGBA{R: 0x11, G: 0x22, B: 0x33, A: 0xff}, "#112233"},
		{"truecolor through a live pointer", &color.RGBA{R: 0x11, G: 0x22, B: 0x33, A: 0xff}, "#112233"},
	}
	for _, tc := range cases {
		if got := colorToWire(tc.c); got != tc.want {
			t.Errorf("%s: colorToWire = %q, want %q", tc.name, got, tc.want)
		}
	}
}
