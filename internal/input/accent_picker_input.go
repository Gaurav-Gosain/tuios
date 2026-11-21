package input

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
)

func handleAccentPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch o.ListKeys.Keys(msg.String(), true) {
	case listnav.Up:
		o.AccentPickerMove(0, -1)
		return o, nil
	case listnav.Down:
		o.AccentPickerMove(0, 1)
		return o, nil
	}
	switch msg.String() {
	case "esc":
		o.CloseAccentPicker()
		return o, nil
	case "enter":
		return o, o.AccentPickerApply()
	case "tab":
		o.AccentPickerFocus(1)
		return o, nil
	case "shift+tab":
		o.AccentPickerFocus(-1)
		return o, nil
	case "backspace":
		o.AccentPickerHexBackspace()
		return o, nil
	case "left", "h":
		o.AccentPickerMove(-1, 0)
		return o, nil
	case "right", "l":
		o.AccentPickerMove(1, 0)
		return o, nil
	case "shift+left":
		o.AccentPickerMoveShift(-1, 0)
		return o, nil
	case "shift+right":
		o.AccentPickerMoveShift(1, 0)
		return o, nil
	case "shift+up":
		o.AccentPickerMoveShift(0, -1)
		return o, nil
	case "shift+down":
		o.AccentPickerMoveShift(0, 1)
		return o, nil
	case "home":
		o.AccentPickerSliderEnd(false)
		return o, nil
	case "end":
		o.AccentPickerSliderEnd(true)
		return o, nil
	}

	if r := []rune(msg.Text); len(r) == 1 {
		if o.AccentPickerHexKey(r[0]) {
			return o, nil
		}
		if r[0] == 'x' {
			return o, o.AccentPickerClearKey()
		}
	}
	return o, nil
}
