package session

import (
	"image/color"
	"strconv"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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
		c, ok := overlay.ParseHex(h)
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

// TestResolveSGR resolves palette colours against a themed palette and leaves
// everything that is not a palette colour as it was. The cases that have
// broken before: a bright background, the 256-colour spelling of a low index,
// a sub-parameter such as "4:3" that was once flattened into a reset, and
// malformed colour fields that must not be guessed into another attribute.
func TestResolveSGR(t *testing.T) {
	pal := mochaPalette()
	for _, tc := range []struct {
		name, in, want string
	}{
		// 107 = bright white = palette index 15 = #a6adc8.
		{"bright background", "\x1b[107m", "\x1b[48;2;166;173;200m"},
		{"256 low index uses the palette", "\x1b[38;5;1m", "\x1b[38;2;243;139;168m"},
		{"sub-parameters preserved", "\x1b[1;4:3;31mMIX\x1b[0m", "\x1b[1;4:3;38;2;243;139;168mMIX\x1b[0m"},
		{"indexed colour cut short", "\x1b[38;5;m", "\x1b[38;5;m"},
		{"sub-parameter where an index belongs", "\x1b[38;5;:9m", "\x1b[38;5;:9m"},
		{"colon introducer", "\x1b[38:2:196m", "\x1b[38:2:196m"},
		{"truecolour left alone", "\x1b[38;2;1;2;3m", "\x1b[38;2;1;2;3m"},
		{"two colours", "\x1b[31;44m", "\x1b[38;2;243;139;168;48;2;137;180;250m"},
		{"bare reset", "\x1b[m", "\x1b[m"},
		{"zero reset", "\x1b[0m", "\x1b[0m"},
		{"empty fields", "\x1b[;m", "\x1b[;m"},
		{"plain text", "plain text", "plain text"},
		{"empty", "", ""},
		{"erase display", "\x1b[2J", "\x1b[2J"},
		{"cursor position", "\x1b[1;1H", "\x1b[1;1H"},
		{"private mode", "\x1b[?25l", "\x1b[?25l"},
		{"mixed content",
			"line \x1b[31mred\x1b[0m and \x1b[1;44mblue bold\x1b[m done",
			"line \x1b[38;2;243;139;168mred\x1b[0m and \x1b[1;48;2;137;180;250mblue bold\x1b[m done"},
	} {
		if got := ResolveSGR(tc.in, pal); got != tc.want {
			t.Errorf("%s: ResolveSGR(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestResolveSGRBrightForegroundUsesBrightSlot(t *testing.T) {
	// A palette whose bright slots differ from the normal ones, so a
	// regression that maps 91 to pal[1] (normal red) instead of pal[9]
	// (bright red) cannot hide behind a theme that repeats its colours
	// in slots 8-15 (catppuccin, among others, does).
	pal := mustPalette(t, []string{
		"#111111", "#222222", "#333333", "#444444",
		"#555555", "#666666", "#777777", "#888888",
		"#999999", "#ff0000", "#00ff00", "#ffff00",
		"#0000ff", "#ff00ff", "#00ffff", "#ffffff",
	})
	got := ResolveSGR("\x1b[91m", pal)
	want := "\x1b[38;2;255;0;0m" // pal[9] = #ff0000, not pal[1] = #222222
	if got != want {
		t.Fatalf("ResolveSGR(91) = %q, want %q (bright slot, not normal slot)", got, want)
	}
}

// TestResolveSGR256EveryIndex resolves each of the 256 indexes against the
// xterm default palette and checks the result against the table worked out
// from its definition: xterm's basic 16, a cube whose nonzero levels are
// 55+40v, and a grey ramp of 8+10n.
func TestResolveSGR256EveryIndex(t *testing.T) {
	basic := [16][3]int{
		{0, 0, 0}, {205, 0, 0}, {0, 205, 0}, {205, 205, 0},
		{0, 0, 238}, {205, 0, 205}, {0, 205, 205}, {229, 229, 229},
		{127, 127, 127}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
		{92, 92, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
	}
	level := func(v int) int {
		if v == 0 {
			return 0
		}
		return 55 + 40*v
	}
	pal := xtermPalette()
	for i := range 256 {
		var r, g, b int
		switch {
		case i < 16:
			r, g, b = basic[i][0], basic[i][1], basic[i][2]
		case i < 232:
			n := i - 16
			r, g, b = level(n/36), level(n/6%6), level(n%6)
		default:
			r = 8 + 10*(i-232)
			g, b = r, r
		}
		in := "\x1b[38;5;" + strconv.Itoa(i) + "m"
		want := "\x1b[38;2;" + strconv.Itoa(r) + ";" + strconv.Itoa(g) + ";" + strconv.Itoa(b) + "m"
		if got := ResolveSGR(in, pal); got != want {
			t.Errorf("ResolveSGR(38;5;%d) = %q, want %q", i, got, want)
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
