package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// themePickerItems returns the theme ids offered by the picker, filtered by the
// current query. "none" (standard terminal colors) is always first.
func (m *OS) themePickerItems() []string {
	all := append([]string{themeNone}, theme.AvailableThemes()...)
	q := strings.ToLower(strings.TrimSpace(m.ThemePickerQuery))
	if q == "" {
		return all
	}
	var out []string
	for _, id := range all {
		if matched, _ := FuzzyMatch(q, id); matched {
			out = append(out, id)
		}
	}
	return out
}

// OpenThemePicker shows the searchable theme picker, remembering the current
// theme so cancel can restore it.
func (m *OS) OpenThemePicker() {
	theme.EnsureRegistry()
	m.ShowThemePicker = true
	m.ThemePickerQuery = ""
	m.ThemePickerScroll = 0
	current := theme.CurrentThemeID()
	if current == "" {
		current = themeNone
	}
	m.ThemePickerOriginal = current

	// Position the selection on the current theme.
	m.ThemePickerSelected = 0
	for i, id := range m.themePickerItems() {
		if id == current {
			m.ThemePickerSelected = i
			break
		}
	}

	// When opened from the settings panel, cascade the picker down-right of it
	// so both are visible and can be dragged as separate panels.
	if m.ShowSettings {
		so := m.overlayOffset("settings")
		m.setOverlayOffset("themepicker", so[0]+10, so[1]+3)
	}
}

// CloseThemePicker hides the picker without changing the applied theme.
func (m *OS) CloseThemePicker() {
	m.ShowThemePicker = false
	m.ThemePickerQuery = ""
}

// CancelThemePicker restores the theme that was active when the picker opened
// and closes it. Used for Esc, so live preview does not stick.
func (m *OS) CancelThemePicker() {
	m.applyTheme(m.ThemePickerOriginal)
	m.persistThemeSelection(m.ThemePickerOriginal)
	m.CloseThemePicker()
}

// ThemePickerMove moves the selection by delta, keeping the scroll window in
// view, and live-previews the newly selected theme.
func (m *OS) ThemePickerMove(delta int) {
	items := m.themePickerItems()
	if len(items) == 0 {
		return
	}
	m.ThemePickerSelected = clampInt(m.ThemePickerSelected+delta, 0, len(items)-1)
	if m.ThemePickerSelected < m.ThemePickerScroll {
		m.ThemePickerScroll = m.ThemePickerSelected
	}
	if m.ThemePickerSelected >= m.ThemePickerScroll+themePickerVisibleRows {
		m.ThemePickerScroll = m.ThemePickerSelected - themePickerVisibleRows + 1
	}
	// Live preview.
	m.applyTheme(items[m.ThemePickerSelected])
}

// ThemePickerRefilter resets the selection after the query changes and previews
// the new top result.
func (m *OS) ThemePickerRefilter() {
	m.ThemePickerSelected = 0
	m.ThemePickerScroll = 0
	if items := m.themePickerItems(); len(items) > 0 {
		m.applyTheme(items[0])
	}
}

// ThemePickerApplySelection commits the selected theme, persists it, and closes.
func (m *OS) ThemePickerApplySelection() {
	items := m.themePickerItems()
	if m.ThemePickerSelected < 0 || m.ThemePickerSelected >= len(items) {
		m.CloseThemePicker()
		return
	}
	sel := items[m.ThemePickerSelected]
	m.applyTheme(sel)
	m.persistThemeSelection(sel)
	m.CloseThemePicker()
}

// persistThemeSelection writes the chosen theme to the config, mapping the
// "none" sentinel to an empty theme name.
func (m *OS) persistThemeSelection(sel string) {
	m.setAppearance(func(a *config.AppearanceConfig) {
		if sel == themeNone {
			a.Theme = ""
		} else {
			a.Theme = sel
		}
	})
	m.persistSettings()
}
