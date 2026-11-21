package app

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

type settingsUndoEntry struct {
	path, label, before string
}

const settingsUndoLimit = 50

func (m *OS) settingDefault(item settingItem) (value, shown string, ok bool) {
	if !item.derived || item.Path == "" {
		return "", "", false
	}
	o, found := config.LookupOption(item.Path)
	if !found {
		return "", "", false
	}
	value = o.Default
	if value == "" && len(o.Accepted) > 0 && !slices.Contains(o.Accepted, "") {
		value = o.Accepted[0]
	}

	shown = value
	switch {
	case o.Type == config.OptionBool:
		on := (value == "true") != settingInverted[item.Path]
		shown = "off"
		if on {
			shown = "on"
		}
	case o.Type == config.OptionInt && o.Percent:
		shown = value + "%"
	case value == "" && len(o.Accepted) > 0:
		shown = o.Accepted[0]
	case value == "":
		shown = item.Unset
		if shown == "" {
			shown = "unset"
		}
	}
	return value, shown, true
}

func (m *OS) settingDiffers(item settingItem) bool {
	if !item.derived && item.differs != nil {
		return item.differs(m)
	}
	def, _, ok := m.settingDefault(item)
	if !ok {
		return false
	}
	o, _ := config.LookupOption(item.Path)
	now := m.optionEffective(item.Path)
	if def == "" && len(o.Accepted) > 0 {
		def = o.Accepted[0]
	}
	if o.Type == config.OptionInt && now == "0" && def != "0" {
		return false
	}
	return now != def
}

func (m *OS) settingsDefaultNote(item settingItem) string {
	if item.reset != nil && m.settingDiffers(item) {
		key := "backspace"
		if m.settingsSearch.open {
			key = "delete"
		}
		return "Default " + item.resetShown + ", " + key + " resets."
	}
	if !item.derived || !m.settingDiffers(item) {
		return ""
	}
	_, shown, _ := m.settingDefault(item)
	key := "backspace"
	if m.settingsSearch.open {
		key = "delete"
	}
	return "Default " + shown + ", " + key + " resets."
}

func (m *OS) settingsRecord(item settingItem, change func()) {
	if !item.derived || item.Path == "" {
		change()
		return
	}
	before := m.optionValue(item.Path)
	change()
	if m.optionValue(item.Path) == before {
		return
	}
	m.settingsUndo = append(m.settingsUndo, settingsUndoEntry{path: item.Path, label: item.Label, before: before})
	if len(m.settingsUndo) > settingsUndoLimit {
		m.settingsUndo = m.settingsUndo[len(m.settingsUndo)-settingsUndoLimit:]
	}
}

// SettingsResetSelected puts the row under the cursor back to its default.
func (m *OS) SettingsResetSelected() tea.Cmd {
	item, ok := m.settingsSelectedItem()
	if !ok {
		return nil
	}
	if item.reset != nil {
		if !m.settingDiffers(item) {
			m.ShowNotification(item.Label+" is already at its default.", "info", m.Settings.NotificationDuration)
			return nil
		}
		before := m.optionValue(item.Path)
		item.reset(m)
		m.settingsUndo = append(m.settingsUndo, settingsUndoEntry{path: item.Path, label: item.Label, before: before})
		m.ShowNotification(item.Label+" follows "+item.resetShown+" again. ctrl+z undoes it.", "info", m.Settings.NotificationDuration)
		return m.persistSettings()
	}
	def, shown, derived := m.settingDefault(item)
	switch {
	case !derived && m.settingDiffers(item):
		m.ShowNotification(item.Label+" has no reset here. Change it with its control.", "info", m.Settings.NotificationDuration)
		return nil
	case !derived:
		m.ShowNotification(item.Label+" has no single default to go back to here.", "info", m.Settings.NotificationDuration)
		return nil
	case !m.settingDiffers(item):
		m.ShowNotification(item.Label+" is already at its default.", "info", m.Settings.NotificationDuration)
		return nil
	}
	m.settingsRecord(item, func() { m.setOption(item.Path, def) })
	m.ShowNotification(item.Label+" is back to "+shown+". ctrl+z undoes it.", "info", m.Settings.NotificationDuration)
	return m.persistSettings()
}

// SettingsUndo takes back the newest change made on the settings page.
func (m *OS) SettingsUndo() tea.Cmd {
	n := len(m.settingsUndo)
	if n == 0 {
		m.ShowNotification("Nothing to undo on the settings page.", "info", m.Settings.NotificationDuration)
		return nil
	}
	e := m.settingsUndo[n-1]
	m.settingsUndo = m.settingsUndo[:n-1]
	if m.UserConfig == nil {
		return nil
	}
	if err := m.setConfigFromRegistry(e.path, e.before); err != nil {
		m.setOption(e.path, m.optionEffectiveOf(e.path, e.before))
	}
	m.ShowNotification("Undid the change to "+e.label+".", "info", m.Settings.NotificationDuration)
	return m.persistSettings()
}

func (m *OS) optionEffectiveOf(path, value string) string {
	o, ok := config.LookupOption(path)
	if ok && value == "" && len(o.Accepted) > 0 {
		if o.Default != "" {
			return o.Default
		}
		return o.Accepted[0]
	}
	return value
}

func differsFromDefault(path string, now func(m *OS) string) func(m *OS) bool {
	return func(m *OS) bool {
		o, ok := config.LookupOption(path)
		if !ok {
			return false
		}
		v := now(m)
		if v == "" || (o.Type == config.OptionInt && v == "0") {
			return false
		}
		def := o.Default
		if def == "" && len(o.Accepted) > 0 {
			def = o.Accepted[0]
		}
		return v != def
	}
}
