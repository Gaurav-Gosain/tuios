package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// TestServedClientsDoNotRunTheThemeHook is a boundary for a served process.
// A tuios ssh or tuios-web process serves many people, and its hook table is
// the operator's. A guest who changes the theme must not run the operator's
// after-theme-change command on the server. The local row is the positive
// half: the same change there runs it once, with the light flag from the
// rule the mode 2031 notice uses.
//
// Negative control: with the m.Client check removed from noteThemeChange, the
// ssh and web rows fail with one firing each.
func TestServedClientsDoNotRunTheThemeHook(t *testing.T) {
	theme.EnsureRegistry()
	t.Cleanup(func() { _ = theme.Initialize(theme.NoneID) })

	for _, tc := range []struct {
		name string
		kind ClientKind
		runs int
	}{
		{"local", ClientLocal, 1},
		{"ssh", ClientSSH, 0},
		{"web", ClientBrowser, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = theme.Initialize("dracula")
			m := hookTestOS(t)
			m.Client = tc.kind
			r := record(t, m)

			m.noteThemeChange() // the first frame only records
			_ = theme.Initialize("catppuccin_latte")
			m.noteThemeChange()
			m.HookManager.Wait()

			r.mu.Lock()
			defer r.mu.Unlock()
			var got []hooks.Context
			for _, ctx := range r.fired {
				if ctx.EventType == hooks.AfterThemeChange {
					got = append(got, ctx)
				}
			}
			if len(got) != tc.runs {
				t.Fatalf("the hook ran %d times for a theme change in a %s client, want %d", len(got), tc.kind, tc.runs)
			}
			if tc.runs == 1 && (got[0].Theme != "catppuccin_latte" || got[0].ThemeLight != "1" || got[0].ThemeBg == "") {
				t.Errorf("the hook got theme %q, bg %q, light %q, want catppuccin_latte, a background and 1",
					got[0].Theme, got[0].ThemeBg, got[0].ThemeLight)
			}
		})
	}
}
