package engine

import (
	"image/color"
	"testing"
)

// TestNamedThemeDetails pins what a named theme gives a caller: the corner
// radius, the theme a later name finds and opaque colours.
func TestNamedThemeDetails(t *testing.T) {
	t.Run("radius", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			radius float64
		}{
			{"nord", 6},
			{"catppuccin-mocha", 6},
			{"pele", 0},
			{"flexoki", 6},
		} {
			th, ok := ThemeNamed(tc.name)
			if !ok {
				t.Fatalf("ThemeNamed(%q) found nothing", tc.name)
			}
			if th.Radius != tc.radius {
				t.Errorf("ThemeNamed(%q).Radius = %v, want %v", tc.name, th.Radius, tc.radius)
			}
		}
	})

	t.Run("lookup", func(t *testing.T) {
		for _, tc := range []struct {
			input string
			bg    color.NRGBA
			fg    color.NRGBA
		}{
			{"nord", color.NRGBA{0x2e, 0x34, 0x40, 0xff}, color.NRGBA{0xd8, 0xde, 0xe9, 0xff}},
			{"Rosé Pine", color.NRGBA{0x1f, 0x1d, 0x2e, 0xff}, color.NRGBA{0xe0, 0xde, 0xf4, 0xff}},
			{"rose_pine", color.NRGBA{0x1f, 0x1d, 0x2e, 0xff}, color.NRGBA{0xe0, 0xde, 0xf4, 0xff}},
			{"  Tokyo Night ", color.NRGBA{0x1a, 0x1b, 0x26, 0xff}, color.NRGBA{0xc0, 0xca, 0xf5, 0xff}},
			{"kanagawa", color.NRGBA{0x1f, 0x1f, 0x28, 0xff}, color.NRGBA{0xdc, 0xd7, 0xba, 0xff}},
		} {
			th, ok := ThemeNamed(tc.input)
			if !ok {
				t.Errorf("ThemeNamed(%q) found nothing", tc.input)
				continue
			}
			if th.Bg != tc.bg || th.Fg != tc.fg {
				t.Errorf("ThemeNamed(%q) Bg, Fg = %v, %v, want %v, %v", tc.input, th.Bg, th.Fg, tc.bg, tc.fg)
			}
		}
		if th, ok := ThemeNamed("no-such-theme"); ok || th != nil {
			t.Errorf("ThemeNamed(%q) = %v, %v, want nil, false", "no-such-theme", th, ok)
		}
	})

	t.Run("titles", func(t *testing.T) {
		titles := map[string]string{}
		for _, nt := range Themes() {
			titles[nt.Name] = nt.Title
		}
		for name, want := range map[string]string{
			"pele":        "pele",
			"nord":        "Nord",
			"rose-pine":   "Rosé Pine",
			"tokyo-night": "Tokyo Night",
		} {
			if got := titles[name]; got != want {
				t.Errorf("Themes() title of %q = %q, want %q", name, got, want)
			}
		}
	})

	t.Run("alpha", func(t *testing.T) {
		themes := Themes()
		if len(themes) != 13 {
			t.Fatalf("Themes() has %d entries, want 13", len(themes))
		}
		for _, nt := range themes {
			th := nt.Theme
			fields := map[string]color.Color{
				"Bg": th.Bg, "Fg": th.Fg, "Muted": th.Muted, "Line": th.Line,
				"Surface": th.Surface, "SurfaceAlt": th.SurfaceAlt, "Border": th.Border,
				"Accent": th.Accent, "Critical": th.Critical,
			}
			for i, c := range th.Series {
				fields["Series["+string(rune('0'+i))+"]"] = c
			}
			for field, c := range fields {
				n, ok := c.(color.NRGBA)
				if !ok {
					t.Errorf("%s %s is %T, want color.NRGBA", nt.Name, field, c)
					continue
				}
				if n.A != 0xff {
					t.Errorf("%s %s alpha = %#x, want 0xff", nt.Name, field, n.A)
				}
			}
		}
	})
}
