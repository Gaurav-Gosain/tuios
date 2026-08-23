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
	case "backspace":
		if len(o.GlyphPickerQuery) > 0 {
			o.GlyphPickerQuery = o.GlyphPickerQuery[:len(o.GlyphPickerQuery)-1]
			o.GlyphPickerRefilter()
		}
	case "ctrl+u":
		o.GlyphPickerQuery = ""
		o.GlyphPickerRefilter()
	default:
		if keyStr == "space" {
			o.GlyphPickerQuery += " "
			o.GlyphPickerRefilter()
		} else if msg.Text != "" {
			o.GlyphPickerQuery += msg.Text
			o.GlyphPickerRefilter()
		} else if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
			o.GlyphPickerQuery += keyStr
			o.GlyphPickerRefilter()
		}
	}
	return o, nil
}
