package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
)

// handleOpenPathPicker is the pick_path action: alt+; in terminal mode, and
// prefix . by default.
func handleOpenPathPicker(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.OpenPathPicker()
}

// handlePathPickerKey takes a key while the path picker is up. The picker owns
// the keyboard: a key it does not use is dropped, never passed to the pane.
//
//   - up, down, ctrl+p and ctrl+n move the cursor. The best match is drawn at
//     the bottom, next to the filter line, so up goes to the next match.
//   - tab walks into the folder under the cursor, or completes a file name
//   - shift+tab, and backspace with nothing typed, go up one folder
//   - enter types the path into the pane and closes the picker
//   - alt+h shows or hides the names that start with a dot
//   - esc, ctrl+c, ctrl+g and the leader close it
//   - any other text goes to the filter
func handlePathPickerKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if isLeaderKey(msg, &o.Settings, o.HostBaseCode(msg)) {
		o.ClosePathPicker()
		return o, nil
	}
	key := commandKey(msg)
	if textedit.IsWordDeleteKey(key) {
		return o, o.PathPickerDeleteWord()
	}
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
	// The list is drawn upside down, so the motion is turned round: the key
	// that moves up the screen moves on to the next match.
	if listKey(o, key, false, listPage, func(delta int) { o.PathPickerMove(-delta) }) {
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
