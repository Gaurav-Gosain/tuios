package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleAccentPickerInput drives the accent swatch picker. It is opened from the
// rail, so it takes both the rail's motion keys and the arrows, and a digit
// picks a swatch outright.
func handleAccentPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		o.CloseAccentPicker()
	case "enter", " ":
		o.AccentPickerApply(o.AccentPickerSelected)
	case "up", "k", "left", "h":
		o.AccentPickerMove(-1)
	case "down", "j", "right", "l":
		o.AccentPickerMove(1)
	case "0", "x":
		o.AccentPickerClear()
	default:
		if len(key) == 1 && key[0] >= '1' && key[0] <= '8' {
			o.AccentPickerApply(int(key[0] - '1'))
		}
	}
	return o, nil
}
