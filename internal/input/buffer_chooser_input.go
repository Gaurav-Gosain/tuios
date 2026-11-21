package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func handlePasteBuffer(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.PasteNewestBuffer()
}

func handleChooseBuffer(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.OpenBufferChooser()
}

func handleBufferChooserInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	keyStr := msg.String()
	if listKey(o, keyStr, true, listPage, o.BufferChooserMove) {
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
