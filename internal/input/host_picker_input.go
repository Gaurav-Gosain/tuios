package input

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleHostPickerInput drives the machine picker: which machine a new
// window's process runs on. The picker's keys are the list picker's (see
// app/list_picker.go); ctrl+c closes it as esc does.
func handleHostPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	cmd, _ := o.ListPickerKey(commandKey(msg), msg)
	return o, cmd
}
