package app

import (
	"math/rand/v2"
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
	if m.ThemePickerShade != themeShadeAll {
		dark := m.ThemePickerShade == themeShadeDark
		kept := all[:1]
		for _, id := range all[1:] {
			if d, ok := theme.ThemeIsDark(id); ok && d == dark {
				kept = append(kept, id)
			}
		}
		all = kept
	}
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
	m.ThemePickerShade, m.ThemePickerDelete, m.ThemePickerPair = themeShadeAll, "", ""
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
	if !m.previewsLive() {
		// Nothing was applied, so there is nothing to restore.
		m.CloseThemePicker()
		return nil
	}
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
	_, visible, _ := m.themePickerLayout()
	if !livePickerMove(&m.ThemePickerSelected, &m.ThemePickerScroll, delta, len(items), visible, m.listWraps()) {
		return
	}
	if m.previewsLive() {
		m.applyTheme(items[m.ThemePickerSelected])
	}
}

// ThemePickerRefilter resets the selection after the query changes and previews
// the new top result.
func (m *OS) ThemePickerRefilter() {
	m.ThemePickerSelected = 0
	m.ThemePickerScroll = 0
	if items := m.themePickerItems(); len(items) > 0 && m.previewsLive() {
		m.applyTheme(items[0])
	}
}

// ThemePickerApplySelection commits the selected theme, persists it, and closes.
func (m *OS) ThemePickerApplySelection() tea.Cmd {
	items := m.themePickerItems()
	if m.ThemePickerSelected < 0 || m.ThemePickerSelected >= len(items) {
		// Nothing to commit, so nothing is closed. CloseThemePicker leaves the
		// applied theme alone, so closing here would leave the live preview
		// from the last query that did match rows on the screen, unpersisted,
		// with no Esc left to press. Staying up keeps both the query and the
		// escape route that reverts.
		m.ShowNotification("Nothing to apply: no theme matches "+m.ThemePickerQuery, "info", m.Settings.NotificationDuration)
		return nil
	}
	sel := items[m.ThemePickerSelected]
	// With a light and dark pair set, the pick is for one of the two, or
	// both. The picker asks before it writes. See ThemePickerAnswerPair.
	// A client that cannot write the config has nothing to ask: the pick
	// applies as it does with no pair.
	if m.UserConfig != nil && m.UserConfig.Appearance.ThemeLight != "" && !m.ConfigReadOnly {
		m.ThemePickerPair = sel
		return nil
	}
	m.applyTheme(sel)
	save := m.persistThemeSelection(sel)
	m.CloseThemePicker()
	return save
}

// ThemePickerAnswerPair answers the question a pick with a pair set asks:
// d writes appearance.theme, l writes appearance.theme_light, b writes both.
// Any other key goes back to the list. The theme that fits the terminal is
// then put in force.
func (m *OS) ThemePickerAnswerPair(key string) tea.Cmd {
	sel := m.ThemePickerPair
	m.ThemePickerPair = ""
	dark, light := key == "d" || key == "b", key == "l" || key == "b"
	if sel == "" || (!dark && !light) {
		return nil
	}
	if dark {
		m.setThemeSelection(sel)
	}
	if light {
		m.setAppearance(func(a *config.AppearanceConfig) {
			// Empty would end the pair, so no theme is written as none.
			if sel == themeNone {
				a.ThemeLight = theme.NoneID
			} else {
				a.ThemeLight = sel
			}
		})
	}
	save := m.persistSettings()
	m.CloseThemePicker()
	// The pick goes in force when it is for the scheme the terminal shows.
	// Else the theme from before the picker opened comes back. This does not
	// go through followSchemeTheme, which a --theme flag turns off: a pick in
	// the picker is the latest explicit choice.
	if (light && theme.SchemeLight()) || (dark && !theme.SchemeLight()) {
		m.applyTheme(sel)
	} else if m.previewsLive() {
		m.applyTheme(m.ThemePickerOriginal)
	}
	m.schemeApplied = m.schemeTheme()
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
		switch {
		case sel == themeNone && theme.OmarchyInstalled():
			// An empty theme would follow Omarchy again on the next start.
			a.Theme = theme.NoneID
		case sel == themeNone:
			a.Theme = ""
		default:
			a.Theme = sel
		}
	})
}

// ThemePickerEdit closes the picker and opens the theme editor on the
// highlighted theme, or with fresh set on a new theme based on it. Closing
// the editor goes back to the theme the picker opened on.
func (m *OS) ThemePickerEdit(fresh bool) {
	id, ok := m.themePickerHighlighted()
	if !ok {
		return
	}
	original := m.ThemePickerOriginal
	m.CloseThemePicker()
	m.OpenThemeEditor(id, fresh, original)
}

// The shades the picker can list: ctrl+f steps through them.
const (
	themeShadeAll = iota
	themeShadeDark
	themeShadeLight
)

// themeShadeNames are the words the picker's title shows for each shade.
var themeShadeNames = [...]string{"all", "dark", "light"}

// ThemePickerCycleShade lists all themes, then the dark ones, then the light
// ones, and back.
func (m *OS) ThemePickerCycleShade() {
	m.ThemePickerShade = (m.ThemePickerShade + 1) % len(themeShadeNames)
	m.ThemePickerRefilter()
}

// ThemePickerShuffle jumps to a random theme of those listed, and previews
// it as an arrow would.
func (m *OS) ThemePickerShuffle() {
	n := len(m.themePickerItems())
	if n < 2 {
		return
	}
	// #nosec G404 - a theme to try, not a secret
	delta := 1 + rand.IntN(n-1)
	m.ThemePickerMove(delta)
}

// ThemePickerCopyID copies the highlighted theme's id.
func (m *OS) ThemePickerCopyID() tea.Cmd {
	id, ok := m.themePickerHighlighted()
	if !ok || id == themeNone {
		return nil
	}
	m.ShowNotification("Copied the theme id "+id, "success", m.Settings.NotificationDuration)
	return m.clipboardWriteCmd(id)
}

// themePickerHighlighted is the theme under the cursor.
func (m *OS) themePickerHighlighted() (string, bool) {
	items := m.themePickerItems()
	if m.ThemePickerSelected < 0 || m.ThemePickerSelected >= len(items) {
		return "", false
	}
	return items[m.ThemePickerSelected], true
}

// ThemePickerAskDelete asks to delete the highlighted theme, if it is a file
// of yours. An edited built-in reverts to the built-in. The question waits in
// the footer for y.
func (m *OS) ThemePickerAskDelete() {
	id, ok := m.themePickerHighlighted()
	yours, edited := theme.ThemeFileOf(id)
	switch {
	case !ok || !yours:
		m.ShowNotification("Only your own theme files can be deleted", "info", m.Settings.NotificationDuration)
	case m.ConfigReadOnly:
		m.ShowNotification("This client cannot change the theme files", "info", m.Settings.NotificationDuration)
	case !edited && len(theme.ThemesBasedOn(id)) > 0:
		// The themes that name it as their base would stop loading. An
		// edited built-in is safe to remove: they fall back to the built-in.
		m.ShowNotification(id+" is the base of "+strings.Join(theme.ThemesBasedOn(id), ", ")+". Delete those first",
			"warning", m.Settings.NotificationWarningDuration)
	default:
		m.ThemePickerDelete = id
	}
}

// ThemePickerConfirmDelete answers the question ThemePickerAskDelete asked.
// A theme of yours that is in use is not deleted: the screen would have no
// theme to draw. An edited built-in in use goes back to the built-in.
func (m *OS) ThemePickerConfirmDelete(yes bool) tea.Cmd {
	id := m.ThemePickerDelete
	m.ThemePickerDelete = ""
	if !yes || id == "" {
		return nil
	}
	// In use means the theme the picker will go back to on Esc, the one the
	// config names. The highlighted theme is only a preview.
	_, edited := theme.ThemeFileOf(id)
	if m.ThemePickerOriginal == id && !edited {
		m.ShowNotification(id+" is in use. Pick another theme, then delete it", "warning", m.Settings.NotificationWarningDuration)
		return nil
	}
	previewed := theme.CurrentThemeID() == id
	if err := theme.DeleteThemeFile(id); err != nil {
		m.ShowNotification("Theme not deleted: "+err.Error(), "error", m.Settings.NotificationWarningDuration)
		return nil
	}
	switch {
	case edited && (previewed || m.ThemePickerOriginal == id):
		m.applyTheme(id) // the built-in, under the same id
	case previewed:
		m.applyTheme(m.ThemePickerOriginal)
	}
	if edited {
		m.ShowNotification(id+" is the built-in theme again", "success", m.Settings.NotificationDuration)
	} else {
		m.ShowNotification("Deleted the theme "+id, "success", m.Settings.NotificationDuration)
	}
	items := m.themePickerItems()
	m.ThemePickerSelected = min(m.ThemePickerSelected, max(len(items)-1, 0))
	return nil
}
