package app

import (
	"os"
	"path/filepath"
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
// ssh and web rows fail with the theme and the glyph set changed. Without the
// LearnMode term, the learn row fails with nothing changed.
func TestServedPickersDoNotPreviewForEveryone(t *testing.T) {
	theme.EnsureRegistry()
	_ = theme.Initialize("dracula")
	glyphs := theme.ActiveGlyphSetID()
	t.Cleanup(func() {
		_ = theme.Initialize(theme.NoneID)
		theme.SetActiveGlyphs(glyphs)
	})

	for _, tc := range []struct {
		name    string
		kind    ClientKind
		learn   bool
		changes bool
	}{
		{"local", ClientLocal, false, true},
		{"ssh", ClientSSH, false, false},
		{"web", ClientBrowser, false, false},
		// The Learn tour is a browser client alone in its process.
		{"learn", ClientBrowser, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = theme.Initialize("dracula")
			theme.SetActiveGlyphs(glyphs)
			m := &OS{Settings: config.Global, Width: 120, Height: 40, Client: tc.kind, LearnMode: tc.learn}
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

// TestServedPickerCannotDeleteThemeFiles is the same boundary for a writer:
// ctrl+d in the picker deletes a file in the operator's themes directory, so a
// served client must not reach it. The local row is the positive half.
//
// Negative control: with the ConfigReadOnly check removed from
// ThemePickerAskDelete, the ssh row deletes the file. With the ThemesBasedOn
// check removed, the base row deletes a file another theme is built on.
func TestServedPickerCannotDeleteThemeFiles(t *testing.T) {
	dir, err := theme.GetThemesDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		kind    ClientKind
		child   bool
		deletes bool
	}{
		{"ssh", ClientSSH, false, false},
		{"local", ClientLocal, false, true},
		// Another file names it as its base, and would stop loading.
		{"local base", ClientLocal, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "doomed.json")
			if err := os.WriteFile(path, []byte(`{"id": "doomed", "base": "nord"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Remove(path) })
			if tc.child {
				kid := filepath.Join(dir, "kid.json")
				if err := os.WriteFile(kid, []byte(`{"id": "kid", "base": "doomed"}`), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Remove(kid) })
			}
			theme.ReloadCustomThemes()
			// A local client previews the theme it highlights, and a refused
			// delete leaves that preview up. Later tests expect no theme.
			t.Cleanup(func() { _ = theme.Initialize(theme.NoneID) })

			m := &OS{Settings: config.Global, Width: 120, Height: 40, Client: tc.kind,
				ConfigReadOnly: tc.kind.Remote()}
			m.UserConfig = config.DefaultConfig()
			m.OpenThemePicker()
			m.ThemePickerQuery = "doomed"
			m.ThemePickerRefilter()
			m.ThemePickerAskDelete()
			m.ThemePickerConfirmDelete(true)

			_, err := os.Stat(path)
			if deleted := os.IsNotExist(err); deleted != tc.deletes {
				t.Errorf("after ctrl+d then y, the theme file deleted = %v, want %v", deleted, tc.deletes)
			}
		})
	}
}
