package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleThemePickerInput handles keyboard input for the theme picker. Selection
// live-previews the theme; Enter commits, Esc restores the original.
func handleThemePickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if listKey(msg.String(), false, listPage, o.ThemePickerMove) {
		return o, nil
	}
	switch msg.String() {
	case "esc":
		return o, o.CancelThemePicker()
	case "enter":
		return o, o.ThemePickerApplySelection()
	default:
		if changed, _ := editFilterQuery(msg, &o.ThemePickerQuery, true); changed {
			o.ThemePickerRefilter()
		}
	}
	return o, nil
}
