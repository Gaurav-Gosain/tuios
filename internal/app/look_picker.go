package app

import (
	"os"

	tea "charm.land/bubbletea/v2"
)

// OpenLookPicker opens a pane that runs `tuios look pick`, the gallery list of
// looks. The pane runs the same command a shell does, so a look chosen here
// goes through the same questions as `tuios look apply`. --hold keeps the pane
// open until the person has read the result.
func (m *OS) OpenLookPicker() tea.Cmd {
	exe, err := os.Executable()
	if err != nil {
		m.ShowNotification("tuios cannot find its own program, so it cannot open the look list", "error", m.Settings.NotificationDuration*2)
		return nil
	}
	m.AddWindow("Choose a look", exe, "look", "pick", "--hold")
	m.EnterTerminalMode()
	return nil
}
