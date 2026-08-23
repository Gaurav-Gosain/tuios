package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleDockEditorInput handles keyboard input for the dock layout editor.
// The arrows select, shifted arrows move the selected component (and carry it
// into the next region off the end of its own), Enter adds or removes.
func handleDockEditorInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch keyStr := msg.String(); keyStr {
	case "esc":
		// Closes, keeping the layout. Every edit here was applied and saved as
		// it was made, so there is nothing pending for Esc to abandon.
		o.CloseDockEditor()
	case "enter", "space":
		return o, o.DockEditorToggle()
	case "up", "ctrl+p", "k":
		o.DockEditorMove(-1)
	case "down", "ctrl+n", "j":
		o.DockEditorMove(1)
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
