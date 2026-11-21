package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func runContextMenuAction(action string, o *app.OS) (*app.OS, tea.Cmd) {
	if action == "" {
		return o, nil
	}
	if name, ok := config.MenuNameOf(action); ok {
		o.OpenSubmenu(name)
		o.ClearMenuTarget()
		return o, nil
	}
	dispatcher := GetDispatcher()
	if !dispatcher.HasAction(action) {
		o.ClearMenuTarget()
		return o, nil
	}
	next, cmd := dispatcher.Dispatch(action, tea.KeyPressMsg{}, o)
	next.ClearMenuTarget()
	return next, cmd
}

func handleContextMenuKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if listKey(o, commandKey(msg), true, listPage, o.ContextMenuMove) {
		return o, nil
	}
	switch commandKey(msg) {
	case "esc", "ctrl+c":
		o.CloseContextMenu()
	case "shift+tab":
		o.ContextMenuMove(-1)
	case "tab":
		o.ContextMenuMove(1)
	case "enter", "space":
		action := o.ContextMenuSelectedAction()
		o.CloseContextMenu()
		return runContextMenuAction(action, o)
	}
	return o, nil
}
