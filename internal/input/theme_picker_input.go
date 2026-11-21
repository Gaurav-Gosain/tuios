package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func handleThemePickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	cmd, _ := o.ListPickerKey(msg.String(), msg)
	return o, cmd
}

func handlePresetPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if listKey(o, msg.String(), true, listPage, o.PresetPickerMove) {
		return o, nil
	}
	switch msg.String() {
	case "esc", "q":
		o.ClosePresetPicker()
	case "enter":
		return o, o.PresetPickerApply()
	}
	return o, nil
}
