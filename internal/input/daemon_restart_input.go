package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleDaemonRestartInput drives the restart dialog. Each answer has one key:
// r restarts, esc cancels, and enter runs the row under the cursor. While the
// restart runs every key is held, since the steps cannot stop halfway.
func handleDaemonRestartInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := commandKey(msg)
	if listKey(o, key, true, listPage, o.DaemonRestartMove) {
		return o, nil
	}
	switch key {
	case "esc", "q":
		return o, o.CloseDaemonRestart()
	case "r":
		return o, o.DaemonRestartActivate(app.RestartRowRestart)
	case "enter", "space":
		return o, o.DaemonRestartActivate(o.DaemonRestartSelected())
	}
	return o, nil
}
