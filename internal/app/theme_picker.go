package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

// themePickerItems returns the theme ids offered by the picker, filtered by the
// current query. "none" (standard terminal colors) is always first.
func (m *OS) themePickerItems() []string {
	all := append([]string{themeNone}, theme.AvailableThemes()...)
	q := strings.ToLower(strings.TrimSpace(m.ThemePickerQuery))
	if q == "" {
		return all
	}
	hits := fuzzy.Filter(q, all)
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Text
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
// and closes it. Used for Esc, so live preview does not stick. It only persists
// when a live preview actually changed the active theme, so a no-op cancel does
// not rewrite config.toml (and cannot overwrite the configured theme).
func (m *OS) CancelThemePicker() tea.Cmd {
	current := theme.CurrentThemeID()
	if current == "" {
		current = themeNone
	}
	m.applyTheme(m.ThemePickerOriginal)
	var save tea.Cmd
	if current != m.ThemePickerOriginal {
		save = m.persistThemeSelection(m.ThemePickerOriginal)
	}
	m.CloseThemePicker()
	return save
}

// ThemePickerMove moves the selection by delta, keeping the scroll window in
// view, and live-previews the newly selected theme.
func (m *OS) ThemePickerMove(delta int) {
	items := m.themePickerItems()
	if len(items) == 0 {
		return
	}
	m.ThemePickerSelected = clampInt(m.ThemePickerSelected+delta, 0, len(items)-1)
	_, visible, _ := m.themePickerLayout()
	if m.ThemePickerSelected < m.ThemePickerScroll {
		m.ThemePickerScroll = m.ThemePickerSelected
	}
	if m.ThemePickerSelected >= m.ThemePickerScroll+visible {
		m.ThemePickerScroll = m.ThemePickerSelected - visible + 1
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
func (m *OS) ThemePickerApplySelection() tea.Cmd {
	items := m.themePickerItems()
	if m.ThemePickerSelected < 0 || m.ThemePickerSelected >= len(items) {
		// Nothing to commit, so nothing is closed. Closing here went through
		// CloseThemePicker, which leaves the applied theme alone: the live
		// preview from the last query that did match rows stayed on the screen,
		// unpersisted, with the picker gone and no way back to it but Esc that
		// was no longer there to press. Staying up keeps both the query and the
		// escape route that reverts.
		m.ShowNotification("Nothing to apply: no theme matches "+m.ThemePickerQuery, "info", config.NotificationDuration)
		return nil
	}
	sel := items[m.ThemePickerSelected]
	m.applyTheme(sel)
	save := m.persistThemeSelection(sel)
	m.CloseThemePicker()
	return save
}

// persistThemeSelection writes the chosen theme to the config, mapping the
// "none" sentinel to an empty theme name.
func (m *OS) persistThemeSelection(sel string) tea.Cmd {
	m.setThemeSelection(sel)
	return m.persistSettings()
}

// setThemeSelection mirrors the chosen theme into the held config without
// writing it out, for the callers whose own save step follows.
func (m *OS) setThemeSelection(sel string) {
	m.setAppearance(func(a *config.AppearanceConfig) {
		if sel == themeNone {
			a.Theme = ""
		} else {
			a.Theme = sel
		}
	})
}
