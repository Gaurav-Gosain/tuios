package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handlePasteBuffer is the paste_buffer action: paste the newest paste buffer
// into the focused pane.
func handlePasteBuffer(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.PasteNewestBuffer()
}

// handleChooseBuffer is the choose_buffer action: list the paste buffers to
// pick one to paste.
func handleChooseBuffer(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.OpenBufferChooser()
}

// handleBufferChooserInput handles keys while the buffer chooser is up: the
// list keys move, enter pastes, d deletes, and esc or q closes.
func handleBufferChooserInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	keyStr := msg.String()
	if listKey(keyStr, true, listPage, o.BufferChooserMove) {
		return o, nil
	}
	switch keyStr {
	case "esc", "q":
		o.CloseBufferChooser()
	case "enter":
		return o, o.BufferChooserActivate(o.BufferChooserSelected())
	case "d", "delete":
		return o, o.BufferChooserDelete()
	}
	return o, nil
}
