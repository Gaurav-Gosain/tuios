package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleGlyphPickerInput handles keyboard input for the glyph-set picker.
// Selection live-previews the set; Enter commits, Esc restores the original.
func handleGlyphPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if listKey(msg.String(), false, listPage, o.GlyphPickerMove) {
		return o, nil
	}
	switch msg.String() {
	case "esc":
		return o, o.CancelGlyphPicker()
	case "enter":
		return o, o.GlyphPickerApplySelection()
	default:
		if changed, _ := editFilterQuery(msg, &o.GlyphPickerQuery, true); changed {
			o.GlyphPickerRefilter()
		}
	}
	return o, nil
}
