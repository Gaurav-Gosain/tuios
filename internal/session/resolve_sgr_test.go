package session

import (
	"image/color"
	"strings"
	"testing"
)

// mochaPalette is catppuccin_mocha's first 8 colours plus bright variants, as
// a stand-in for the palette a themed client would send. Red is #f38ba8, the
// exact example from issue #135.
func mochaPalette() [16]color.Color {
	hex := []string{
		"#45475a", "#f38ba8", "#a6e3a1", "#f9e2af",
		"#89b4fa", "#f5c2e7", "#94e2d5", "#bac2de",
		"#585b70", "#f38ba8", "#a6e3a1", "#f9e2af",
		"#89b4fa", "#f5c2e7", "#94e2d5", "#a6adc8",
	}
	var pal [16]color.Color
	for i, h := range hex {
		c, ok := parseHexColor(h)
		if !ok {
			panic("bad test palette: " + h)
		}
		pal[i] = c
	}
	return pal
}

func mustPalette(t *testing.T, hex []string) [16]color.Color {
	t.Helper()
	pal, err := paletteFromParams(hex)
	if err != nil {
		t.Fatalf("paletteFromParams(%v) failed: %v", hex, err)
	}
	return pal
}

func TestResolveSGRBasicForeground(t *testing.T) {
	pal := mochaPalette()
	got := ResolveSGR("\x1b[31mred\x1b[0m", pal)
	// Red resolves to #f38ba8 → 38;2;243;139;168. The reset stays a reset.
	want := "\x1b[38;2;243;139;168mred\x1b[0m"
	if got != want {
		t.Fatalf("ResolveSGR(31) = %q, want %q", got, want)
	}
}

func TestResolveSGRBasicBackground(t *testing.T) {
	pal := mochaPalette()
	got := ResolveSGR("\x1b[44m", pal)
	want := "\x1b[48;2;137;180;250m" // #89b4fa
	if got != want {
		t.Fatalf("ResolveSGR(44) = %q, want %q", got, want)
	}
}

func TestResolveSGRBrightForeground(t *testing.T) {
	pal := mochaPalette()
	// 91 = bright red = palette index 9 = #f38ba8.
	got := ResolveSGR("\x1b[91m", pal)
	want := "\x1b[38;2;243;139;168m"
	if got != want {
		t.Fatalf("ResolveSGR(91) = %q, want %q", got, want)
	}
}

func TestResolveSGRBrightBackground(t *testing.T) {
	pal := mochaPalette()
	// 107 = bright white = palette index 15 = #a6adc8.
	got := ResolveSGR("\x1b[107m", pal)
	want := "\x1b[48;2;166;173;200m"
	if got != want {
		t.Fatalf("ResolveSGR(107) = %q, want %q", got, want)
	}
}

func TestResolveSGR256LowIndexUsesPalette(t *testing.T) {
	pal := mochaPalette()
	// 38;5;1 is the 256-colour spelling of index 1; the theme palette owns it.
	got := ResolveSGR("\x1b[38;5;1m", pal)
	want := "\x1b[38;2;243;139;168m"
	if got != want {
		t.Fatalf("ResolveSGR(38;5;1) = %q, want %q", got, want)
	}
}

func TestResolveSGR256CubeAndRampResolved(t *testing.T) {
	pal := mochaPalette()
	// Indices at or above 16 belong to the standard 256-colour cube and grey
	// ramp, whose levels ride the index itself, so they resolve to fixed RGB
	// values no palette redefines. 196 is the top red corner, 208 the classic
	// orange, 232/255 the ramp's ends.
	cases := []struct{ in, want string }{
		{"\x1b[38;5;196m", "\x1b[38;2;255;0;0m"},
		{"\x1b[38;5;208m", "\x1b[38;2;255;135;0m"},
		{"\x1b[48;5;208m", "\x1b[48;2;255;135;0m"},
		{"\x1b[38;5;232m", "\x1b[38;2;8;8;8m"},
		{"\x1b[38;5;255m", "\x1b[38;2;238;238;238m"},
	}
	for _, c := range cases {
		if got := ResolveSGR(c.in, pal); got != c.want {
			t.Fatalf("ResolveSGR(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestResolveSGRSubparametersPreserved pins the fix for fields SGR reads as a
// single parameter with variants: "4:3" (curly underline) must survive
// verbatim while its neighbours still resolve. Flattening it to 0 once turned
// the sequence into a reset that killed bold and underline.
func TestResolveSGRSubparametersPreserved(t *testing.T) {
	pal := mochaPalette()
	got := ResolveSGR("\x1b[1;4:3;31mMIX\x1b[0m", pal)
	want := "\x1b[1;4:3;38;2;243;139;168mMIX\x1b[0m"
	if got != want {
		t.Fatalf("ResolveSGR(sub-params) = %q, want %q", got, want)
	}
}

// TestResolveSGRMalformedFieldsPreserved checks fields that cannot be colour
// parameters are handed back untouched rather than guessed into some other
// attribute: an indexed colour cut short before its index, a sub-parameter
// where an index belongs, and the colon spelling of the introducer itself.
func TestResolveSGRMalformedFieldsPreserved(t *testing.T) {
	pal := mochaPalette()
	for _, in := range []string{
		"\x1b[38;5;m",
		"\x1b[38;5;:9m",
		"\x1b[38:2:196m",
	} {
		if got := ResolveSGR(in, pal); got != in {
			t.Fatalf("ResolveSGR(%q) = %q, want unchanged %q", in, got, in)
		}
	}
}

func TestResolveSGRTrueColourLeftAlone(t *testing.T) {
	pal := mochaPalette()
	in := "\x1b[38;2;1;2;3m"
	if got := ResolveSGR(in, pal); got != in {
		t.Fatalf("ResolveSGR(truecolour) = %q, want unchanged %q", got, in)
	}
}

func TestResolveSGRAttributesSurvive(t *testing.T) {
	pal := mochaPalette()
	got := ResolveSGR("\x1b[1;31m", pal)
	want := "\x1b[1;38;2;243;139;168m"
	if got != want {
		t.Fatalf("ResolveSGR(1;31) = %q, want %q", got, want)
	}
}

func TestResolveSGRMultipleColours(t *testing.T) {
	pal := mochaPalette()
	got := ResolveSGR("\x1b[31;44m", pal)
	want := "\x1b[38;2;243;139;168;48;2;137;180;250m"
	if got != want {
		t.Fatalf("ResolveSGR(31;44) = %q, want %q", got, want)
	}
}

func TestResolveSGRResetAndEmpty(t *testing.T) {
	pal := mochaPalette()
	for _, in := range []string{"\x1b[m", "\x1b[0m", "\x1b[;m", "plain text", ""} {
		if got := ResolveSGR(in, pal); got != in {
			t.Fatalf("ResolveSGR(%q) = %q, want unchanged %q", in, got, in)
		}
	}
}

func TestResolveSGRNonSGRCSIIntact(t *testing.T) {
	pal := mochaPalette()
	for _, in := range []string{"\x1b[2J", "\x1b[1;1H", "\x1b[?25l"} {
		if got := ResolveSGR(in, pal); got != in {
			t.Fatalf("ResolveSGR(%q) = %q, want unchanged %q", in, got, in)
		}
	}
}

func TestResolveSGRMixedContent(t *testing.T) {
	pal := mochaPalette()
	in := "line \x1b[31mred\x1b[0m and \x1b[1;44mblue bold\x1b[m done"
	got := ResolveSGR(in, pal)
	want := "line \x1b[38;2;243;139;168mred\x1b[0m and \x1b[1;48;2;137;180;250mblue bold\x1b[m done"
	if got != want {
		t.Fatalf("ResolveSGR(mixed) = %q, want %q", got, want)
	}
}

func TestParseHexColor(t *testing.T) {
	cases := []struct {
		in   string
		want color.RGBA
		ok   bool
	}{
		{"#f38ba8", color.RGBA{0xf3, 0x8b, 0xa8, 0xff}, true},
		{"f38ba8", color.RGBA{0xf3, 0x8b, 0xa8, 0xff}, true},
		{"#fab", color.RGBA{0xff, 0xaa, 0xbb, 0xff}, true},
		{"#f38ba", color.RGBA{}, false},
		{"#gggggg", color.RGBA{}, false},
		{"", color.RGBA{}, false},
	}
	for _, c := range cases {
		got, ok := parseHexColor(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatalf("parseHexColor(%q) = %v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestPaletteFromParams(t *testing.T) {
	// Empty → xterm default, no error.
	pal, err := paletteFromParams(nil)
	if err != nil {
		t.Fatalf("paletteFromParams(nil) error: %v", err)
	}
	if len(pal) != 16 {
		t.Fatalf("xterm palette len = %d, want 16", len(pal))
	}

	// Wrong length → error.
	if _, err := paletteFromParams([]string{"#000000"}); err == nil {
		t.Fatal("paletteFromParams(1 entry) should error")
	}

	// Bad hex → error.
	bad := make([]string, 16)
	for i := range bad {
		bad[i] = "#000000"
	}
	bad[7] = "not-a-colour"
	if _, err := paletteFromParams(bad); err == nil {
		t.Fatal("paletteFromParams(bad hex) should error")
	}

	// Good palette round-trips through ResolveSGR.
	good := make([]string, 16)
	for i := range good {
		good[i] = "#000000"
	}
	good[1] = "#f38ba8"
	pal, err = paletteFromParams(good)
	if err != nil {
		t.Fatalf("paletteFromParams(good) error: %v", err)
	}
	if got := ResolveSGR("\x1b[31m", pal); got != "\x1b[38;2;243;139;168m" {
		t.Fatalf("resolved with palette = %q", got)
	}
}

func TestXtermPaletteIsRGB(t *testing.T) {
	pal := xtermPalette()
	for i, c := range pal {
		r, g, b, a := c.RGBA()
		if a == 0 {
			t.Fatalf("xterm colour %d has zero alpha", i)
		}
		if r == 0 && g == 0 && b == 0 && i != 0 {
			t.Fatalf("xterm colour %d is black; palette not populated", i)
		}
	}
	// The xterm red (index 1) is a known RGB; confirm it serialises to a
	// true-colour unit rather than an index.
	got := ResolveSGR("\x1b[31m", pal)
	if !strings.HasPrefix(got, "\x1b[38;2;") {
		t.Fatalf("xterm red resolved to %q, want 38;2;...", got)
	}
}
