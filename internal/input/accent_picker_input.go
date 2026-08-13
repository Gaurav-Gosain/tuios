package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleAccentPickerInput drives the colour picker. Tab walks the four controls
// (hue strip, shades grid, hex field, harmony chips) and the arrows move within
// whichever one has the keyboard, so everything the mouse can reach the
// keyboard can reach too. Enter applies, esc cancels and puts back the colour
// the window had.
//
// Which axis a motion key moves is the picker's business, not this handler's:
// it takes the step and hands it over.
func handleAccentPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
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
	case "up", "k":
		o.AccentPickerMove(0, -1)
		return o, nil
	case "down", "j":
		o.AccentPickerMove(0, 1)
		return o, nil
	}

	// A hex digit types into the field wherever the keyboard is, which is the
	// shortest path from "I have a hex" to the colour it names. Anything the
	// field will not take falls through to the clear key.
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
