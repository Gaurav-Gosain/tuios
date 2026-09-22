package overlay

import (
	"image/color"
	"testing"
)

func TestParseHex(t *testing.T) {
	rgb := func(r, g, b uint8) color.RGBA { return color.RGBA{R: r, G: g, B: b, A: 0xff} }
	cases := []struct {
		in   string
		want color.RGBA
		ok   bool
	}{
		{"#f38ba8", rgb(0xf3, 0x8b, 0xa8), true},
		{"f38ba8", rgb(0xf3, 0x8b, 0xa8), true},
		{"#F38BA8", rgb(0xf3, 0x8b, 0xa8), true},
		{"#fab", rgb(0xff, 0xaa, 0xbb), true},
		{"f0a", rgb(0xff, 0x00, 0xaa), true},
		{"#000000", rgb(0, 0, 0), true},
		{"#ffffff", rgb(0xff, 0xff, 0xff), true},
		{"#f38ba", color.RGBA{}, false},
		{"#gggggg", color.RGBA{}, false},
		{"#ff00gg", color.RGBA{}, false},
		{"#fg0", color.RGBA{}, false},
		{"##fab", color.RGBA{}, false},
		{" #fab", color.RGBA{}, false},
		{"#fab ", color.RGBA{}, false},
		{"+fab", color.RGBA{}, false},
		{"#", color.RGBA{}, false},
		{"", color.RGBA{}, false},
		{"#ff00aa00", color.RGBA{}, false},
	}
	for _, c := range cases {
		got, ok := ParseHex(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseHex(%q) = %v, %v, want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestHex(t *testing.T) {
	cases := []struct {
		in   color.RGBA
		want string
	}{
		{color.RGBA{A: 0xff}, "#000000"},
		{color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, "#ffffff"},
		{color.RGBA{R: 0xf3, G: 0x8b, B: 0xa8, A: 0xff}, "#f38ba8"},
		{color.RGBA{R: 0x0a, G: 0xb0, B: 0x01}, "#0ab001"},
	}
	for _, c := range cases {
		if got := Hex(c.in); got != c.want {
			t.Errorf("Hex(%v) = %q, want %q", c.in, got, c.want)
		}
	}
	// Every channel value survives a round trip through the parser.
	for v := range 256 {
		c := color.RGBA{R: uint8(v), G: uint8(255 - v), B: uint8(v ^ 0x5a), A: 0xff}
		if got, ok := ParseHex(Hex(c)); !ok || got != c {
			t.Errorf("ParseHex(Hex(%v)) = %v, %v", c, got, ok)
		}
	}
}
