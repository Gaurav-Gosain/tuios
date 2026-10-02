//go:build slim

package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// tuios-slim has no effect picker. This stands in for effect_picker_input.go.
func handleEffectPickerInput(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.CancelEffectPicker()
	return o, nil
}
