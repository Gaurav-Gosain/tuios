package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleCommandPaletteInput handles keyboard input when the command palette is
// open, through the list picker's keys (see app/list_picker.go). A page is the
// rows on screen, and the rebind key edits the binding of the row's action.
func handleCommandPaletteInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	cmd, _ := o.ListPickerKey(msg.String(), msg)
	return o, cmd
}
