package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleLayoutPickerInput handles keyboard input when the layout picker is
// open: the name to save under in its save mode, the list picker's keys (see
// app/list_picker.go) in its load mode, with ctrl+d to delete a layout.
func handleLayoutPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.LayoutPickerMode == "save" {
		o.LayoutSaveKey(msg)
		return o, nil
	}
	cmd, _ := o.ListPickerKey(commandKey(msg), msg)
	return o, cmd
}
