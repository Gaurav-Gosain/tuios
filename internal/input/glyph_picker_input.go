package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleGlyphPickerInput handles keyboard input for the glyph-set picker,
// through the list picker's keys (see app/list_picker.go). Selection
// live-previews the set; Enter commits, Esc restores the original.
func handleGlyphPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	cmd, _ := o.ListPickerKey(msg.String(), msg)
	return o, cmd
}
