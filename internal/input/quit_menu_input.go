package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func handleQuitMenuKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if listKey(o, commandKey(msg), true, listPage, o.QuitMenuMove) {
		return o, nil
	}
	switch commandKey(msg) {
	case "esc", "n", "ctrl+c":
		o.CloseQuitMenu()
	case "shift+tab":
		o.QuitMenuMove(-1)
	case "tab":
		o.QuitMenuMove(1)
	case "enter", "space":
		return o, o.QuitMenuActivate(o.QuitMenuCursor())
	case "q":
		return o, o.QuitMenuActivate(0)
	case "d":
		return runQuitMenuKind(o, app.QuitDetach)
	case "s":
		return runQuitMenuKind(o, app.QuitSwitchSession)
	case "x":
		return runQuitMenuKind(o, app.QuitKillGoNext, app.QuitKillAndQuit)
	case "w":
		return runQuitMenuKind(o, app.QuitDeleteWorktree)
	}
	return o, nil
}

func runQuitMenuKind(o *app.OS, kinds ...app.QuitMenuKind) (*app.OS, tea.Cmd) {
	idx := o.QuitMenuIndexOfKind(kinds...)
	if idx < 0 {
		return o, nil
	}
	return o, o.QuitMenuActivate(idx)
}
