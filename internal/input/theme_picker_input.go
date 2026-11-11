package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleThemePickerInput handles keyboard input for the theme picker. The keys
// are the list picker's (see app/list_picker.go), with the picker's ctrl chords
// and its pending questions read first. Selection live-previews the theme;
// Enter commits, Esc restores the original.
func handleThemePickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	cmd, _ := o.ListPickerKey(msg.String(), msg)
	return o, cmd
}

// handlePresetPickerInput handles a key in the keybind preset picker.
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
