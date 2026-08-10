package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleQuitMenuKey drives the open quit menu. The menu is modal while it is
// up, so every key stops here.
//
// The accelerators address rows by what they do, not by position: d detaches,
// s opens the switcher, x runs the kill row. A second q runs the first row,
// which in a daemon session is Detach, so the old "qq" muscle memory that used
// to kill a session now safely detaches it.
func handleQuitMenuKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch msg.String() {
	case "esc", "n", "ctrl+c":
		o.CloseQuitMenu()
	case "up", "k", "shift+tab":
		o.QuitMenuMove(-1)
	case "down", "j", "tab":
		o.QuitMenuMove(1)
	case "enter", " ":
		return o, o.QuitMenuActivate(o.QuitMenuSelected)
	case "q":
		return o, o.QuitMenuActivate(0)
	case "d":
		return runQuitMenuKind(o, app.QuitDetach)
	case "s":
		return runQuitMenuKind(o, app.QuitSwitchSession)
	case "x":
		return runQuitMenuKind(o, app.QuitKillGoNext, app.QuitKillAndQuit)
	}
	return o, nil
}

// runQuitMenuKind activates the first quit-menu row of any of the given kinds,
// or does nothing when the current row set has none of them.
func runQuitMenuKind(o *app.OS, kinds ...app.QuitMenuKind) (*app.OS, tea.Cmd) {
	idx := o.QuitMenuIndexOfKind(kinds...)
	if idx < 0 {
		return o, nil
	}
	return o, o.QuitMenuActivate(idx)
}
