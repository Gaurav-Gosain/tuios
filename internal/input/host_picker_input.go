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
		o.CloseHostPicker()
		return o, nil

	case "enter":
		if len(filtered) == 0 {
			return o, nil
		}
		if o.HostPickerSelected < 0 || o.HostPickerSelected >= len(filtered) {
			return o, nil
		}
		return o, o.ChooseHost(filtered[o.HostPickerSelected])

	case "up", "ctrl+p":
		o.HostPickerMove(-1)
		return o, nil

	case "down", "ctrl+n":
		o.HostPickerMove(1)
		return o, nil
	}

	// A space and any typed rune, the way the session switcher takes them. The
	// gate here was printable ASCII only, so a machine with a space in its
	// name could not be searched for past its first word.
	if changed, _ := editFilterQuery(msg, &o.HostPickerQuery, true); changed {
		// The list changes with the query, so the cursor goes back to the top
		// rather than staying on a row that may no longer be there.
		o.HostPickerSelected = 0
	}
	return o, nil
}
