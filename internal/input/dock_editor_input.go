package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func handleDockEditorInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if listKey(o, msg.String(), true, listPage, o.DockEditorMove) {
		return o, nil
	}
	switch keyStr := msg.String(); keyStr {
	case "esc":
		o.CloseDockEditor()
	case "enter", "space":
		return o, o.DockEditorToggle()
	case "shift+up", "K":
		return o, o.DockEditorShift(-1)
	case "shift+down", "J":
		return o, o.DockEditorShift(1)
	case "r":
		return o, o.DockEditorReset()
	case "u":
		return o, o.DockEditorRevert()
	}
	return o, nil
}
