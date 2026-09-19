package input

import (
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleThemePickerInput handles keyboard input for the theme picker. Selection
// live-previews the theme; Enter commits, Esc restores the original.
func handleThemePickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch keyStr := msg.String(); keyStr {
	case "esc":
		return o, o.CancelThemePicker()
	case "enter":
		return o, o.ThemePickerApplySelection()
	case "up", "ctrl+p":
		o.ThemePickerMove(-1)
	case "down", "ctrl+n":
		o.ThemePickerMove(1)
	case "backspace":
		if len(o.ThemePickerQuery) > 0 {
			// A rune at a time. Taking a byte off splits a multi-byte character
			// and leaves invalid UTF-8 in the query.
			_, size := utf8.DecodeLastRuneInString(o.ThemePickerQuery)
			o.ThemePickerQuery = o.ThemePickerQuery[:len(o.ThemePickerQuery)-size]
			o.ThemePickerRefilter()
		}
	case "ctrl+u":
		o.ThemePickerQuery = ""
		o.ThemePickerRefilter()
	default:
		if keyStr == "space" {
			o.ThemePickerQuery += " "
			o.ThemePickerRefilter()
		} else if msg.Text != "" {
			o.ThemePickerQuery += msg.Text
			o.ThemePickerRefilter()
		} else if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
			o.ThemePickerQuery += keyStr
			o.ThemePickerRefilter()
		}
	}
	return o, nil
}
