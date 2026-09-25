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
