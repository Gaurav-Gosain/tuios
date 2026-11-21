package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func handleSessionCloseInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if listKey(o, commandKey(msg), true, listPage, o.SessionCloseMove) {
		return o, nil
	}
	switch commandKey(msg) {
	case "esc", "q":
		o.CloseSessionClose()
	case "enter", "space":
		return o, o.SessionCloseActivate(o.SessionCloseCursor())
	}
	return o, nil
}
