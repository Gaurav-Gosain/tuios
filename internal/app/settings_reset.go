package app

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A settings row used to give no sign of whether it had been changed, and
// once it had, no way back but remembering what it was. A row built from the
// registry now marks itself when it differs from the default, says what the
// default is under the list, and goes back to it on a key; and every change
// made on the page can be taken back, newest first.

// settingsUndoEntry is one change made on the settings page: the path and the
// value it held before.
type settingsUndoEntry struct {
	path, label, before string
}

// settingsUndoLimit bounds the undo list. A settings page is not an editor,
// and nobody walks back further than this.
const settingsUndoLimit = 50

// settingDefault is the value a derived row goes back to, and what the row
// shows for it. ok is false for a row that is not derived.
//
// The value is the registry default except for the enums whose default is the
// empty string without the empty string being a value they accept: for those
// the built-in is the first value they accept (TestEnumDefaultIsTheFirstAccepted
// pins it), and writing that is what resetting them can do.
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

// settingDiffers reports whether a derived row's value in force is not its
// default.
func (m *OS) settingDiffers(item settingItem) bool {
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
		// An int left at zero is unset, which is the default.
		return false
	}
	return now != def
}

// settingsDefaultNote is the line added to a changed row's description: what
// the default is and the key that goes back to it.
func (m *OS) settingsDefaultNote(item settingItem) string {
	if !m.settingDiffers(item) {
		return ""
	}
	_, shown, _ := m.settingDefault(item)
	key := "backspace"
	if m.settingsSearch.open {
		key = "delete"
	}
	return "Default " + shown + ", " + key + " resets."
}

// settingsRecord runs change against a derived row and, when the value moved,
// puts what it was on the undo list.
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
	def, shown, derived := m.settingDefault(item)
	switch {
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
	// The value as it was stored, which for a few enums is the empty string
	// the registry will not take back. Those go to what the empty string
	// meant, which is the same setting in force.
	if err := m.setConfigFromRegistry(e.path, e.before); err != nil {
		m.setOption(e.path, m.optionEffectiveOf(e.path, e.before))
	}
	m.ShowNotification("Undid the change to "+e.label+".", "info", m.Settings.NotificationDuration)
	return m.persistSettings()
}

// optionEffectiveOf is optionEffective for a stored value other than the one
// held now.
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
