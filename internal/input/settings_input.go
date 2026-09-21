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
		return o, o.SettingsAdjust(-1)
	case "right", "l":
		return o, o.SettingsAdjust(1)
	case "enter", "space":
		return o, o.SettingsActivate()
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
	number := o.SettingsEditingNumber()
	switch msg.String() {
	case "esc":
		o.SettingsEditCancel()
	case "enter":
		return o, o.SettingsEditCommit()
	case "backspace":
		o.SettingsEditBackspace()
	case "ctrl+u":
		o.SettingsEditClear()
	case "left", "right", "up", "down":
		// The slider half of the number editor. A text field has nothing for
		// these to do, so they are left alone there.
		if !number {
			return o, nil
		}
		dir := 1
		if msg.String() == "left" || msg.String() == "down" {
			dir = -1
		}
		o.SettingsEditNumberSlide(dir)
	default:
		switch {
		case number:
			// Digits and a leading minus only. A number editor that accepts
			// letters is one that refuses the value on Enter for a reason the
			// user cannot see while typing it.
			if t := msg.Text; t != "" && isNumberEntry(t) {
				o.SettingsEditAppend(t)
			}
		case msg.String() == "space":
			o.SettingsEditAppend(" ")
		case msg.Text != "":
			o.SettingsEditAppend(msg.Text)
		}
	}
	return o, nil
}

// isNumberEntry reports whether typed text belongs in a number field.
func isNumberEntry(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}
