package input

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleEffectPickerInput drives the screen saver effect picker. Selection
// previews the effect over the captured screen; Enter commits it, Esc closes
// and leaves the setting alone. The keys are the list picker's (see
// app/list_picker.go); a move returns the preview's first frame.
func handleEffectPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	cmd, _ := o.ListPickerKey(commandKey(msg), msg)
	return o, cmd
}
