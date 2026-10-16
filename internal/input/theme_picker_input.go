package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleThemePickerInput handles keyboard input for the theme picker. Selection
// live-previews the theme; Enter commits, Esc restores the original.
func handleThemePickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// A pick with a light and dark pair set waits for d, l or b. Any other
	// key goes back to the list, and is used up.
	if o.ThemePickerPair != "" {
		return o, o.ThemePickerAnswerPair(msg.String())
	}
	// A delete waits for y. Any other key keeps the theme, and is used up.
	if o.ThemePickerDelete != "" {
		return o, o.ThemePickerConfirmDelete(msg.String() == "y")
	}
	if listKey(o, msg.String(), false, listPage, o.ThemePickerMove) {
		return o, nil
	}
	// The commands are ctrl chords, because a typed letter filters. ctrl+n and
	// ctrl+p move the list, and ctrl+u clears the query.
	switch msg.String() {
	case "ctrl+f":
		o.ThemePickerCycleShade()
		return o, nil
	case "ctrl+r":
		o.ThemePickerShuffle()
		return o, nil
	case "ctrl+y":
		return o, o.ThemePickerCopyID()
	case "ctrl+d":
		o.ThemePickerAskDelete()
		return o, nil
	case "ctrl+e":
		o.ThemePickerEdit(false)
		return o, nil
	case "ctrl+o":
		o.ThemePickerEdit(true)
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

// handlePresetPickerInput handles a key in the keybind preset picker.
func handlePresetPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if listKey(o, msg.String(), true, listPage, o.PresetPickerMove) {
		return o, nil
	}
	switch msg.String() {
	case "esc", "q":
		o.ClosePresetPicker()
	case "enter":
		return o, o.PresetPickerApply()
	}
	return o, nil
}
