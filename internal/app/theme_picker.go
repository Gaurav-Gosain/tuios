package app

import (
	"math/rand/v2"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

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
	q := strings.ToLower(strings.TrimSpace(m.themeList.Query.String()))
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
	m.themeList.Reset()
	m.ThemePickerShade, m.ThemePickerDelete, m.ThemePickerPair = themeShadeAll, "", ""
	current := theme.CurrentThemeID()
	if current == "" {
		current = themeNone
	}
	m.ThemePickerOriginal = current

	for i, id := range m.themePickerItems() {
		if id == current {
			m.themeList.Cursor = i
			break
		}
	}

	if m.ShowSettings {
		so := m.overlayOffset("settings")
		m.setOverlayOffset("themepicker", so[0]+10, so[1]+3)
	}
}

// CloseThemePicker hides the picker without changing the applied theme.
func (m *OS) CloseThemePicker() {
	m.ShowThemePicker = false
	m.themeList.Reset()
}

// CancelThemePicker restores the theme that was active when the picker opened
// and closes it. Used for Esc, so live preview does not stick. It only persists
// when a live preview actually changed the active theme, so a no-op cancel does
// not rewrite config.toml (and cannot overwrite the configured theme).
func (m *OS) CancelThemePicker() tea.Cmd {
	if !m.previewsLive() {
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

func (m *OS) themePickerPreview() {
	items := m.themePickerItems()
	if c := m.themeList.Cursor; c >= 0 && c < len(items) && m.previewsLive() {
		m.applyTheme(items[c])
	}
}

// ThemePickerMove moves the selection by delta and previews the new row.
func (m *OS) ThemePickerMove(delta int) { themePicker().move(m, delta) }

func themePicker() *listPicker {
	return &listPicker{
		kind:  "themepicker",
		state: func(m *OS) *listnav.State { return &m.themeList },
		count: func(m *OS) int { return len(m.themePickerItems()) },
		visible: func(m *OS) int {
			_, visible, _ := m.themePickerLayout()
			return visible
		},
		keys:   themePickerKeys,
		accept: func(m *OS) tea.Cmd { return m.ThemePickerApplySelection() },
		moved: func(m *OS) tea.Cmd {
			m.themePickerPreview()
			return nil
		},
		refiltered: func(m *OS) tea.Cmd {
			m.themePickerPreview()
			return nil
		},
		close:    func(m *OS) tea.Cmd { return m.CancelThemePicker() },
		clearKey: true,
	}
}

func themePickerKeys(m *OS, key string) (tea.Cmd, bool) {
	if m.ThemePickerPair != "" {
		return m.ThemePickerAnswerPair(key), true
	}
	if m.ThemePickerDelete != "" {
		return m.ThemePickerConfirmDelete(key == "y"), true
	}
	switch key {
	case "ctrl+f":
		m.ThemePickerCycleShade()
	case "ctrl+r":
		m.ThemePickerShuffle()
	case "ctrl+y":
		return m.ThemePickerCopyID(), true
	case "ctrl+d":
		m.ThemePickerAskDelete()
	case "ctrl+e":
		m.ThemePickerEdit(false)
	case "ctrl+o":
		m.ThemePickerEdit(true)
	default:
		return nil, false
	}
	return nil, true
}

// ThemePickerApplySelection commits the selected theme, persists it, and closes.
func (m *OS) ThemePickerApplySelection() tea.Cmd {
	items := m.themePickerItems()
	if m.themeList.Cursor < 0 || m.themeList.Cursor >= len(items) {
		m.ShowNotification("Nothing to apply: no theme matches "+m.themeList.Query.String(), "info", m.Settings.NotificationDuration)
		return nil
	}
	sel := items[m.themeList.Cursor]
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
			if sel == themeNone {
				a.ThemeLight = theme.NoneID
			} else {
				a.ThemeLight = sel
			}
		})
	}
	save := m.persistSettings()
	m.CloseThemePicker()
	if (light && theme.SchemeLight()) || (dark && !theme.SchemeLight()) {
		m.applyTheme(sel)
	} else if m.previewsLive() {
		m.applyTheme(m.ThemePickerOriginal)
	}
	m.schemeApplied = m.schemeTheme()
	return save
}

func (m *OS) persistThemeSelection(sel string) tea.Cmd {
	m.setThemeSelection(sel)
	return m.persistSettings()
}

func (m *OS) setThemeSelection(sel string) {
	m.setAppearance(func(a *config.AppearanceConfig) {
		switch {
		case sel == themeNone && theme.OmarchyInstalled():
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

const (
	themeShadeAll = iota
	themeShadeDark
	themeShadeLight
)

var themeShadeNames = [...]string{"all", "dark", "light"}

// ThemePickerCycleShade lists all themes, then the dark ones, then the light
// ones, and back.
func (m *OS) ThemePickerCycleShade() {
	m.ThemePickerShade = (m.ThemePickerShade + 1) % len(themeShadeNames)
	_ = themePicker().refilter(m)
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

func (m *OS) themePickerHighlighted() (string, bool) {
	items := m.themePickerItems()
	if m.themeList.Cursor < 0 || m.themeList.Cursor >= len(items) {
		return "", false
	}
	return items[m.themeList.Cursor], true
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
		m.applyTheme(id)
	case previewed:
		m.applyTheme(m.ThemePickerOriginal)
	}
	if edited {
		m.ShowNotification(id+" is the built-in theme again", "success", m.Settings.NotificationDuration)
	} else {
		m.ShowNotification("Deleted the theme "+id, "success", m.Settings.NotificationDuration)
	}
	items := m.themePickerItems()
	m.themeList.Cursor = min(m.themeList.Cursor, max(len(items)-1, 0))
	return nil
}
