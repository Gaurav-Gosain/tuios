package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func handleOpenPathPicker(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.OpenPathPicker()
}

func handlePathPickerKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if isLeaderKey(msg, &o.Settings, o.HostBaseCode(msg)) {
		o.ClosePathPicker()
		return o, nil
	}
	key := commandKey(msg)
	switch key {
	case "esc", "ctrl+c", "ctrl+g":
		o.ClosePathPicker()
		return o, nil
	case "enter", "ctrl+j", "ctrl+m":
		o.PathPickerAccept()
		return o, nil
	case "tab", "ctrl+i":
		return o, o.PathPickerTab()
	case "shift+tab":
		return o, o.PathPickerUp()
	case "backspace", "ctrl+h":
		return o, o.PathPickerBackspace()
	case "alt+h":
		o.PathPickerToggleHidden()
		return o, nil
	case "ctrl+u":
		return o, o.PathPickerClearQuery()
	}
	if listKey(o, key, false, listPage, func(delta int) { o.PathPickerMove(-delta) }) {
		return o, nil
	}
	if o.PathPickerKey(key) {
		return o, nil
	}
	if msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper) == 0 {
		return o, o.PathPickerType(msg.Text)
	}
	if key == "space" {
		return o, o.PathPickerType(" ")
	}
	return o, nil
}
