package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func handleLayoutPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.LayoutPickerMode == "save" {
		o.LayoutSaveKey(msg)
		return o, nil
	}
	cmd, _ := o.ListPickerKey(commandKey(msg), msg)
	return o, cmd
}
