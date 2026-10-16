package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// TestServedPickersDoNotPreviewForEveryone is a privacy boundary. The theme and
// the glyph set are process-wide, and a tuios ssh or tuios-web process serves
// many clients, so a guest scrolling a picker repainted every other guest and
// the operator. A served client moves the cursor only. The local row is the
// positive half: the same moves there do preview.
//
// Negative control: with previewsLive returning true for every client, the
// ssh and web rows fail with the theme and the glyph set changed.
func TestServedPickersDoNotPreviewForEveryone(t *testing.T) {
	theme.EnsureRegistry()
	_ = theme.Initialize("dracula")
	glyphs := theme.ActiveGlyphSetID()
	t.Cleanup(func() {
		_ = theme.Initialize(theme.NoneID)
		theme.SetActiveGlyphs(glyphs)
	})

	for _, tc := range []struct {
		kind    ClientKind
		changes bool
	}{
		{ClientLocal, true},
		{ClientSSH, false},
		{ClientBrowser, false},
	} {
		t.Run(tc.kind.String(), func(t *testing.T) {
			_ = theme.Initialize("dracula")
			theme.SetActiveGlyphs(glyphs)
			m := &OS{Settings: config.Global, Width: 120, Height: 40, Client: tc.kind}
			m.UserConfig = config.DefaultConfig()

			m.OpenThemePicker()
			m.ThemePickerQuery = "nord"
			m.ThemePickerRefilter()
			m.ThemePickerMove(1)
			if changed := theme.CurrentThemeID() != "dracula"; changed != tc.changes {
				t.Errorf("theme after moving the picker is %q; changed = %v, want %v",
					theme.CurrentThemeID(), changed, tc.changes)
			}
			m.CancelThemePicker()
			if got := theme.CurrentThemeID(); got != "dracula" {
				t.Errorf("theme after cancel is %q, want dracula", got)
			}

			m.OpenGlyphPicker()
			m.GlyphPickerMove(1)
			if changed := theme.ActiveGlyphSetID() != glyphs; changed != tc.changes {
				t.Errorf("glyph set after moving the picker is %q; changed = %v, want %v",
					theme.ActiveGlyphSetID(), changed, tc.changes)
			}
		})
	}
}
