package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleGotoPicker is the goto_picker action.
func handleGotoPicker(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenGotoPicker()
	return o, nil
}

// handleGotoPickerInput takes a key while the go-to picker is up, through the
// list picker's keys (see app/list_picker.go), with its row actions read
// first.
func handleGotoPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	cmd, _ := o.ListPickerKey(msg.String(), msg)
	return o, cmd
}
