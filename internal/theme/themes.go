package theme

import (
	"image/color"
	"log"
	"sort"
	"sync"

	"charm.land/lipgloss/v2"
	tint "github.com/lrstanley/bubbletint/v2"
)

// defaultSwatch is the preview palette shown for the "no theme" option (the
// standard xterm-ish bright colors).
var defaultSwatch = []color.Color{
	lipgloss.Color("#ff5555"), lipgloss.Color("#f1fa8c"), lipgloss.Color("#50fa7b"),
	lipgloss.Color("#8be9fd"), lipgloss.Color("#6272a4"), lipgloss.Color("#bd93f9"),
	lipgloss.Color("#f8f8f2"), lipgloss.Color("#282a36"),
}

// ThemeSwatch returns a small, representative set of colors for a theme id, for
// previewing it in the theme picker. Unknown or empty ids return the default
// palette.
func ThemeSwatch(id string) []color.Color {
	EnsureRegistry()
	t, ok := tint.GetTint(id)
	if !ok || t == nil {
		return defaultSwatch
	}
	return []color.Color{
		t.BrightRed, t.BrightYellow, t.BrightGreen,
		t.BrightCyan, t.BrightBlue, t.BrightPurple,
		t.Fg, t.Bg,
	}
}

var ensureRegistryOnce sync.Once

// EnsureRegistry populates the tint registry with the built-in tints and any
// custom themes, without enabling theming or changing the current theme. This
// lets the settings page list and preview themes even when the session started
// with no theme selected.
func EnsureRegistry() {
	ensureRegistryOnce.Do(func() {
		tint.NewDefaultRegistry()
		if themesDir, err := GetThemesDir(); err == nil {
			if _, err := LoadCustomThemes(themesDir); err != nil {
				log.Printf("Warning: error loading custom themes: %v", err)
			}
		}
	})
}

// AvailableThemes returns the sorted list of registered theme IDs (built-in
// tints plus any custom themes loaded from the user's themes directory). The
// list is used by the in-app settings page to cycle themes.
func AvailableThemes() []string {
	EnsureRegistry()
	ids := tint.TintIDs()
	sorted := make([]string, len(ids))
	copy(sorted, ids)
	sort.Strings(sorted)
	return sorted
}

// CurrentThemeID returns the ID of the active theme, or an empty string when
// theming is disabled.
func CurrentThemeID() string {
	t := Current()
	if t == nil {
		return ""
	}
	return t.ID
}
