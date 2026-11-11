package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleLauncherInput handles keyboard input when the launcher is open, through
// the list picker's keys (see app/list_picker.go): Enter runs the program,
// Tab types its command line into a shell pane.
func handleLauncherInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	cmd, _ := o.ListPickerKey(msg.String(), msg)
	return o, cmd
}
