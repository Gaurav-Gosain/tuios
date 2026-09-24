package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleEffectPickerInput handles keyboard input for the screen saver effect
// picker. Selection previews the effect over the captured screen; Enter commits
// it, Esc closes and leaves the setting alone.
//
// The keys are the theme and glyph pickers' keys. What differs is that every
// move returns a command: the preview is an animation, so a move that starts
// one has a first frame to schedule.
func handleEffectPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	var moved tea.Cmd
	if listKey(msg.String(), false, listPage, func(d int) { moved = o.EffectPickerMove(d) }) {
		return o, moved
	}
	switch keyStr := msg.String(); keyStr {
	case "esc":
		o.CancelEffectPicker()
	case "enter":
		return o, o.EffectPickerApplySelection()
	case "backspace":
		return o, o.EffectPickerBackspace()
	case "ctrl+u":
		return o, o.EffectPickerClearQuery()
	default:
		if keyStr == "space" {
			return o, o.EffectPickerType(" ")
		} else if msg.Text != "" {
			return o, o.EffectPickerType(msg.Text)
		} else if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
			return o, o.EffectPickerType(keyStr)
		}
	}
	return o, nil
}
