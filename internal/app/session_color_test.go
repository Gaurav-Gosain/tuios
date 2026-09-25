package app

import (
	"image/color"
	"testing"
)

// TestSessionAccentVocabulary pins what a session accent may be written as. The
// daemon records the string verbatim and has never read it, so anything already
// on disk has to keep meaning what it meant, and anything unreadable has to read
// as unset rather than as a colour nobody chose.
func TestSessionAccentVocabulary(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Accent
		ok   bool
	}{
		{"cyan", SlotAccent(13), true},
		{"CYAN", SlotAccent(13), true},
		{"bright cyan", SlotAccent(6), true},
		{"bright-cyan", SlotAccent(6), true},
		{"Bright_Cyan", SlotAccent(6), true},
		{"magenta", SlotAccent(12), true},
		{"purple", SlotAccent(12), true},
		{"#89b4fa", RGBAccent(color.RGBA{R: 0x89, G: 0xb4, B: 0xfa, A: 0xff}), true},
		{"#f0a", RGBAccent(color.RGBA{R: 0xff, G: 0x00, B: 0xaa, A: 0xff}), true},
		{"", Accent{}, false},
		{"   ", Accent{}, false},
		{"chartreuse", Accent{}, false},
		{"#12345", Accent{}, false},
	} {
		got, ok := ParseAccent(tc.in)
		if ok != tc.ok || (tc.ok && got != tc.want) {
			t.Errorf("ParseAccent(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
