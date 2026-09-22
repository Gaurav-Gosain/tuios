package input

import (
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
	default:
		if changed, _ := editFilterQuery(msg, &o.ThemePickerQuery, true); changed {
			o.ThemePickerRefilter()
		}
	}
	return o, nil
}
