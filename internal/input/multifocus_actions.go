package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// Multifocus actions. Neither has a default key. A user binds them in any
// [keybindings] section, prefix_mode most usefully, since the set is changed
// from terminal mode where the typing happens.
const (
	actionToggleMultifocusActive = "toggle_multifocus_active"
	actionToggleMultifocusAll    = "toggle_multifocus_all"
)

func (d *ActionDispatcher) registerMultifocusHandlers() {
	d.Register(actionToggleMultifocusActive, handleToggleMultifocusActive)
	d.Register(actionToggleMultifocusAll, handleToggleMultifocusAll)
}

// handleToggleMultifocusActive puts the focused pane in the multifocus set, or
// takes it out. It is the palette's "Toggle multifocus".
func handleToggleMultifocusActive(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ToggleMultifocus(o.FocusedWindow)
	return o, nil
}

// handleToggleMultifocusAll puts every visible pane on the workspace in the
// set, or clears the set when all of them are in it already.
func handleToggleMultifocusAll(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ToggleMultifocusAll()
	return o, nil
}
