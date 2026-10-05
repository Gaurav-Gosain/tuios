package guibridge

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/colorprofile"
)

// The renderer paints the chrome only from what the bridge exports, so a
// theme must arrive with its terminal colours, both chrome palettes and the
// agent colours, and a light theme must say it is light.
func TestCurrentThemeExportsWhatTheRendererPaints(t *testing.T) {
	theme.SetColorProfile(colorprofile.TrueColor)
	for _, tc := range []struct {
		name  string
		light bool
	}{{"dracula", false}, {"catppuccin_latte", true}} {
		if err := theme.Initialize(tc.name); err != nil {
			t.Fatal(err)
		}
		th := CurrentTheme()
		if th.Name != tc.name {
			t.Fatalf("name %q, want %q", th.Name, tc.name)
		}
		if len(th.Names) < 50 {
			t.Errorf("only %d themes listed", len(th.Names))
		}
		if th.Light != tc.light {
			t.Errorf("%s: light %v", tc.name, th.Light)
		}
		if th.Terminal.Bg == "" || th.Terminal.ANSI[1] == "" {
			t.Errorf("%s: terminal colours missing: %+v", tc.name, th.Terminal)
		}
		for _, k := range []string{"Canvas", "Panel", "Surface", "Card", "Fg", "FgDim", "FgMute", "Accent", "Hover", "Edge"} {
			if th.UI[k] == "" || th.Ground[k] == "" {
				t.Errorf("%s: %s missing (ui %q ground %q)", tc.name, k, th.UI[k], th.Ground[k])
			}
		}
		if th.Agent["needs_input"] == "" || th.BorderFocused == "" {
			t.Errorf("%s: agent or border colours missing", tc.name)
		}
	}
}
