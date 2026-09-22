package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleGlyphPickerInput handles keyboard input for the glyph-set picker.
// Selection live-previews the set; Enter commits, Esc restores the original.
func handleGlyphPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch keyStr := msg.String(); keyStr {
	case "esc":
		return o, o.CancelGlyphPicker()
	case "enter":
		return o, o.GlyphPickerApplySelection()
	case "up", "ctrl+p":
		o.GlyphPickerMove(-1)
	case "down", "ctrl+n":
		o.GlyphPickerMove(1)
	default:
		if changed, _ := editFilterQuery(msg, &o.GlyphPickerQuery, true); changed {
			o.GlyphPickerRefilter()
		}
	}
	return o, nil
}
