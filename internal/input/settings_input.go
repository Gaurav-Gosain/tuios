package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleSettingsInput handles keyboard input while the settings overlay is open.
// Changes apply live and are persisted by the OS as they are made.
func handleSettingsInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
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
