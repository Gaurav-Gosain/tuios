package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleLayoutPickerInput handles keyboard input when the layout picker is open.
func handleLayoutPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	keyStr := msg.String()

	if o.LayoutPickerMode == "save" {
		return handleLayoutSaveInput(msg, keyStr, o)
	}
	return handleLayoutLoadInput(msg, keyStr, o)
}

// handleLayoutSaveInput handles input for the layout save name prompt.
func handleLayoutSaveInput(msg tea.KeyPressMsg, keyStr string, o *app.OS) (*app.OS, tea.Cmd) {
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

	default:
		editFilterQuery(msg, &o.LayoutSaveBuffer, true)
		return o, nil
	}
}

// handleLayoutLoadInput handles input for the layout load/browse mode.
func handleLayoutLoadInput(msg tea.KeyPressMsg, keyStr string, o *app.OS) (*app.OS, tea.Cmd) {
	filtered := app.FilterLayoutTemplates(o.LayoutPickerItems, o.LayoutPickerQuery)

	if listKey(o, keyStr, false, listPage, o.LayoutPickerMove) {
		return o, nil
	}

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

	case "ctrl+d":
		// Deleting a layout is behind a modifier, which is where the session
		// switcher and the keybind manager put theirs.
		//
		// A bare "d", gated on the query being empty, would fire in exactly
		// the state the picker opens in: typing "docs" to filter would delete
		// the selected layout and then filter on "ocs". A filter keystroke
		// must never destroy saved state.
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
		if changed, _ := editFilterQuery(msg, &o.LayoutPickerQuery, true); changed {
			o.LayoutPickerSelected = 0
			o.LayoutPickerScroll = 0
		}
		return o, nil
	}
}
