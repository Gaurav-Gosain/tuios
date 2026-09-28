package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleSessionCloseInput drives the close-session confirmation. There is no
// one-key yes: the answer is a selection and then enter, which is what makes a
// dialog that always appears still worth appearing.
func handleSessionCloseInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if listKey(commandKey(msg), true, listPage, o.SessionCloseMove) {
		return o, nil
	}
	switch commandKey(msg) {
	case "esc", "q":
		o.CloseSessionClose()
	case "enter", "space":
		return o, o.SessionCloseActivate(o.SessionCloseSelected)
	}
	return o, nil
}
