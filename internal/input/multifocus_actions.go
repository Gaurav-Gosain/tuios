package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

const (
	actionToggleMultifocusActive = "toggle_multifocus_active"
	actionToggleMultifocusAll    = "toggle_multifocus_all"
)

func (d *ActionDispatcher) registerMultifocusHandlers() {
	d.Register(actionToggleMultifocusActive, handleToggleMultifocusActive)
	d.Register(actionToggleMultifocusAll, handleToggleMultifocusAll)
	d.Register(actionCloseWorkspace, handleCloseWorkspace)
}

const actionCloseWorkspace = "close_workspace"

func handleCloseWorkspace(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenWorkspaceClose()
	return o, nil
}

func handleToggleMultifocusActive(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ToggleMultifocus(o.FocusedWindow)
	return o, nil
}

func handleToggleMultifocusAll(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ToggleMultifocusAll()
	return o, nil
}
