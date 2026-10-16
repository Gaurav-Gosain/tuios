package theme

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// The ways the colour parser can go wrong, and the test that holds each:
//
//   - A value a config already holds changes colour: every hex form must draw
//     what lipgloss.Color drew for it. TestParseColorForms.
//   - A name lands on the wrong slot: purple and magenta must both be 5, the
//     bright_ forms 8 higher, and an index 0-15 the same slot as its name.
//     TestParseColorForms.
//   - A role reads the palette field of another meaning: error is Warn, not
//     Warning. TestParseColorForms.
//   - A role or slot keeps the old theme's colour after a theme change, or
//     does not step down to the terminal's own slot at 16 colours.
//     TestNamesFollowTheThemeAndTheDepth.
//   - A role loop in a theme file recurses forever. TestAChromeLoopFailsTheFile.
//   - A renderer pays for parsing on every frame: the memo and the slots
//     are read without a parse; the dock and rail benchmarks hold allocs/op.

// TestParseColorForms is the vocabulary, one row per form.
func TestParseColorForms(t *testing.T) {
	pal := overlay.Palette{
		Accent: lipgloss.Color("#000001"), AccentBright: lipgloss.Color("#000002"),
		Success: lipgloss.Color("#000003"), Warning: lipgloss.Color("#000004"),
		Warn: lipgloss.Color("#000005"), Info: lipgloss.Color("#000006"),
		Surface: lipgloss.Color("#000007"), Canvas: lipgloss.Color("#000008"),
		Panel: lipgloss.Color("#000009"), Card: lipgloss.Color("#00000a"),
		Fg: lipgloss.Color("#00000b"), FgDim: lipgloss.Color("#00000c"), FgMute: lipgloss.Color("#00000d"),
		Depth: overlay.Depth16,
	}
	for _, tc := range []struct {
		in   string
		want color.Color // on pal, at 16 colours; nil for an error
	}{
		{"#7dcfff", lipgloss.Color("#7dcfff")},
		{"#7DCFFF", lipgloss.Color("#7dcfff")},
		{"#7cf", lipgloss.Color("#77ccff")},
		{" #7dcfff ", lipgloss.Color("#7dcfff")},
		{"5", ansi.BasicColor(5)},
		{"magenta", ansi.BasicColor(5)},
		{"purple", ansi.BasicColor(5)},
		{"bright_magenta", ansi.BasicColor(13)},
		{"Bright-Purple", ansi.BasicColor(13)},
		{"bright black", ansi.BasicColor(8)},
		{"15", ansi.BasicColor(15)},
		{"16", ansi.IndexedColor(16)},
		{"255", ansi.IndexedColor(255)},
		{"accent", pal.Accent},
		{"accent_bright", pal.AccentBright},
		{"success", pal.Success},
		{"warning", pal.Warning},
		{"error", pal.Warn},
		{"info", pal.Info},
		{"surface", pal.Surface},
		{"canvas", pal.Canvas},
		{"panel", pal.Panel},
		{"card", pal.Card},
		{"text", pal.Fg},
		{"dim", pal.FgDim},
		{"muted", pal.FgMute},
		{"charple", lipgloss.Color("#6b50ff")},
		{"Ash", lipgloss.Color("#ecebf0")},
		{"256", nil},
		{"-1", nil},
		{"#12345", nil},
		{"#ggg", nil},
		{"7dcfff", nil},
		{"pink", nil},
		{"theme", nil},
		{"off", nil},
	} {
		spec, err := ParseColor(tc.in)
		if tc.want == nil {
			if err == nil {
				t.Errorf("ParseColor(%q) = %+v, want an error", tc.in, spec)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseColor(%q): %v", tc.in, err)
			continue
		}
		if got := spec.On(&pal); got != tc.want {
			t.Errorf("ParseColor(%q) on the palette = %v, want %v", tc.in, got, tc.want)
		}
	}
	if spec, err := ParseColor(""); err != nil || !spec.IsZero() || spec.On(&pal) != nil {
		t.Errorf("ParseColor(\"\") = %+v, %v; want unset, no error, no colour", spec, err)
	}
}

// TestNamesFollowTheThemeAndTheDepth: a slot or a role is resolved on the
// theme in force, so the same value draws the new theme's colour after a
// switch, and at 16 colours it is the terminal's own slot.
//
// Negative control: caching the first answer of slotColor in a package
// variable keeps theme one's magenta after the switch.
func TestNamesFollowTheThemeAndTheDepth(t *testing.T) {
	prev := ColorProfile()
	t.Cleanup(func() { SetColorProfile(prev) })
	SetColorProfile(colorprofile.TrueColor)

	one := writeThemeFile(t, map[string]any{"id": "one", "bright_purple": "#bb9af7", "bright_blue": "#7aa2f7"})
	two := writeThemeFile(t, map[string]any{"id": "two", "bright_purple": "#ff79c6", "bright_blue": "#bd93f9"})

	restore := useTheme(t, one)
	defer restore()
	generation.Add(1)
	if got := hexOf(t, ConfigColor("bright_magenta")); got != "#bb9af7" {
		t.Errorf("bright_magenta on theme one = %s, want #bb9af7", got)
	}
	if got := hexOf(t, ConfigColor("accent")); got != "#7aa2f7" {
		t.Errorf("accent on theme one = %s, want #7aa2f7", got)
	}

	useTheme(t, two)
	generation.Add(1)
	if got := hexOf(t, ConfigColor("bright_magenta")); got != "#ff79c6" {
		t.Errorf("bright_magenta after the switch = %s, want theme two's #ff79c6", got)
	}
	if got := hexOf(t, ConfigColor("accent")); got != "#bd93f9" {
		t.Errorf("accent after the switch = %s, want theme two's #bd93f9", got)
	}
	if got := ConfigColor("#123456"); got != lipgloss.Color("#123456") {
		t.Errorf("a hex colour changed with the theme: %v", got)
	}

	SetColorProfile(colorprofile.ANSI)
	if got := ConfigColor("bright_magenta"); got != color.Color(ansi.BasicColor(13)) {
		t.Errorf("bright_magenta at 16 colours = %v, want slot 13", got)
	}
	if got := ConfigColor("accent"); got != color.Color(ansi.BasicColor(12)) {
		t.Errorf("accent at 16 colours = %v, want slot 12", got)
	}
}
