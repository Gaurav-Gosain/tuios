package input

import (
	"unicode/utf8"

	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleLayoutPickerInput handles keyboard input when the layout picker is open.
func handleLayoutPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	keyStr := msg.String()

	if o.LayoutPickerMode == "save" {
		return handleLayoutSaveInput(keyStr, o)
	}
	return handleLayoutLoadInput(msg, keyStr, o)
}

// handleLayoutSaveInput handles input for the layout save name prompt.
func handleLayoutSaveInput(keyStr string, o *app.OS) (*app.OS, tea.Cmd) {
	switch keyStr {
	case "esc":
		o.ShowLayoutPicker = false
		o.LayoutSaveBuffer = ""
		return o, nil

	case "enter":
		name := o.LayoutSaveBuffer
		if name == "" {
			o.ShowNotification("Layout name cannot be empty", "warning", o.Settings.NotificationDuration)
			return o, nil
		}
		if err := app.SaveLayoutTemplate(name, o); err != nil {
			o.ShowNotification("Failed to save layout: "+err.Error(), "error", o.Settings.NotificationDuration)
		} else {
			o.ShowNotification("Layout saved: "+name, "success", o.Settings.NotificationDuration)
		}
		o.ShowLayoutPicker = false
		o.LayoutSaveBuffer = ""
		return o, nil

	case "backspace":
		if len(o.LayoutSaveBuffer) > 0 {
			_, size := utf8.DecodeLastRuneInString(o.LayoutSaveBuffer)
			o.LayoutSaveBuffer = o.LayoutSaveBuffer[:len(o.LayoutSaveBuffer)-size]
		}
		return o, nil

	case "ctrl+u":
		o.LayoutSaveBuffer = ""
		return o, nil

	default:
		if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
			o.LayoutSaveBuffer += keyStr
		}
		return o, nil
	}
}

// handleLayoutLoadInput handles input for the layout load/browse mode.
func handleLayoutLoadInput(msg tea.KeyPressMsg, keyStr string, o *app.OS) (*app.OS, tea.Cmd) {
	filtered := app.FilterLayoutTemplates(o.LayoutPickerItems, o.LayoutPickerQuery)

	switch keyStr {
	case "esc":
		o.ShowLayoutPicker = false
		o.LayoutPickerQuery = ""
		o.LayoutPickerSelected = 0
		o.LayoutPickerScroll = 0
		return o, nil

	case "enter":
		if len(filtered) > 0 && o.LayoutPickerSelected < len(filtered) {
			selected := filtered[o.LayoutPickerSelected]
			app.ApplyLayoutTemplate(selected, o)
			o.ShowLayoutPicker = false
			o.LayoutPickerQuery = ""
			o.LayoutPickerSelected = 0
			o.LayoutPickerScroll = 0
			o.ShowNotification("Layout applied: "+selected.Name, "success", o.Settings.NotificationDuration)
		}
		return o, nil

	case "up", "ctrl+p":
		o.LayoutPickerMove(-1)
		return o, nil

	case "down", "ctrl+n":
		o.LayoutPickerMove(1)
		return o, nil

	case "backspace":
		if len(o.LayoutPickerQuery) > 0 {
			_, size := utf8.DecodeLastRuneInString(o.LayoutPickerQuery)
			o.LayoutPickerQuery = o.LayoutPickerQuery[:len(o.LayoutPickerQuery)-size]
			o.LayoutPickerSelected = 0
			o.LayoutPickerScroll = 0
		}
		return o, nil

	case "ctrl+u":
		o.LayoutPickerQuery = ""
		o.LayoutPickerSelected = 0
		o.LayoutPickerScroll = 0
		return o, nil

	case "ctrl+d":
		// Deleting a layout is behind a modifier, which is where the session
		// switcher and the keybind manager put theirs.
		//
		// It used to be a bare "d", gated on the query being empty, which is
		// exactly the state the picker opens in. Typing "docs" to filter
		// deleted the selected layout and then filtered on "ocs". It was the
		// one place in the app where a filter keystroke destroyed saved state.
		{
			if len(filtered) > 0 && o.LayoutPickerSelected < len(filtered) {
				selected := filtered[o.LayoutPickerSelected]
				if err := app.DeleteLayoutTemplate(selected.Name); err != nil {
					o.ShowNotification("Failed to delete layout: "+err.Error(), "error", o.Settings.NotificationDuration)
				} else {
					o.ShowNotification("Layout deleted: "+selected.Name, "info", o.Settings.NotificationDuration)
					// Refresh the list
					templates, _ := app.LoadLayoutTemplates()
					o.LayoutPickerItems = templates
					if o.LayoutPickerSelected >= len(o.LayoutPickerItems) {
						o.LayoutPickerSelected = max(len(o.LayoutPickerItems)-1, 0)
					}
				}
			}
		}
		return o, nil

	default:
		// A space and anything a person can type, including characters that
		// are more than one byte. The filter used to take printable ASCII
		// only, so a layout with a space in its name could not be searched
		// for past the first word.
		if keyStr == "space" {
			o.LayoutPickerQuery += " "
			o.LayoutPickerSelected = 0
			o.LayoutPickerScroll = 0
			return o, nil
		}
		if msg.Text != "" && !strings.ContainsFunc(msg.Text, unicode.IsControl) {
			o.LayoutPickerQuery += msg.Text
			o.LayoutPickerSelected = 0
			o.LayoutPickerScroll = 0
		}
		return o, nil
	}
}
