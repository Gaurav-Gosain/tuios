package input

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleHostPickerInput drives the machine picker: which machine a new
// window's process runs on.
func handleHostPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	filtered := app.FilterHostPickerItems(o.HostPickerItems, o.HostPickerQuery)

	switch msg.String() {
	case "esc", "ctrl+c":
		o.ShowHostPicker = false
		o.HostPickerQuery = ""
		return o, nil

	case "enter":
		if len(filtered) == 0 {
			return o, nil
		}
		if o.HostPickerSelected < 0 || o.HostPickerSelected >= len(filtered) {
			return o, nil
		}
		return o, o.ChooseHostForNewWindow(filtered[o.HostPickerSelected])

	case "up", "ctrl+p":
		if o.HostPickerSelected > 0 {
			o.HostPickerSelected--
		}
		return o, nil

	case "down", "ctrl+n":
		if o.HostPickerSelected < len(filtered)-1 {
			o.HostPickerSelected++
		}
		return o, nil

	case "backspace":
		if len(o.HostPickerQuery) > 0 {
			o.HostPickerQuery = o.HostPickerQuery[:len(o.HostPickerQuery)-1]
			// The list grows again as letters come off, and a cursor left
			// where a shorter list ended would be pointing past it.
			o.HostPickerSelected = 0
		}
		return o, nil

	case "ctrl+u":
		o.HostPickerQuery = ""
		o.HostPickerSelected = 0
		return o, nil
	}

	if s := msg.String(); len(s) == 1 && s[0] >= 32 && s[0] <= 126 {
		o.HostPickerQuery += s
		// Typing narrows the list, so the cursor goes back to the top rather
		// than staying on a row that may no longer be there.
		o.HostPickerSelected = 0
	}
	return o, nil
}
