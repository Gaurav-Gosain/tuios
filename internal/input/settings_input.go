package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleSettingsInput handles keyboard input while the settings overlay is open.
// Changes apply live and are persisted by the OS as they are made.
func handleSettingsInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SettingsEditActive() {
		return handleSettingsEditInput(msg, o)
	}
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		o.CloseSettings()
	case "up", "k":
		o.SettingsMoveUp()
	case "down", "j":
		o.SettingsMoveDown()
	case "left", "h":
		o.SettingsAdjust(-1)
	case "right", "l":
		o.SettingsAdjust(1)
	case "enter", "space":
		o.SettingsActivate()
	case "tab", "]":
		o.SettingsNextCategory()
	case "shift+tab", "[":
		o.SettingsPrevCategory()
	}
	return o, nil
}

// handleSettingsEditInput handles keystrokes while a text setting is being
// edited inline. Enter commits, Esc cancels, and printable input is appended to
// the buffer.
func handleSettingsEditInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch msg.String() {
	case "esc":
		o.SettingsEditCancel()
	case "enter":
		o.SettingsEditCommit()
	case "backspace":
		o.SettingsEditBackspace()
	case "ctrl+u":
		o.SettingsEditClear()
	default:
		if msg.String() == "space" {
			o.SettingsEditAppend(" ")
		} else if msg.Text != "" {
			o.SettingsEditAppend(msg.Text)
		}
	}
	return o, nil
}
