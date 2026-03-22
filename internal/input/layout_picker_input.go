package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// handleLayoutPickerInput handles keyboard input when the layout picker is open.
func handleLayoutPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	keyStr := msg.String()

	if o.LayoutPickerMode == "save" {
		return handleLayoutSaveInput(keyStr, o)
	}
	return handleLayoutLoadInput(keyStr, o)
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
			o.ShowNotification("Layout name cannot be empty", "warning", config.NotificationDuration)
			return o, nil
		}
		if err := app.SaveLayoutTemplate(name, o); err != nil {
			o.ShowNotification("Failed to save layout: "+err.Error(), "error", config.NotificationDuration)
		} else {
			o.ShowNotification("Layout saved: "+name, "success", config.NotificationDuration)
		}
		o.ShowLayoutPicker = false
		o.LayoutSaveBuffer = ""
		return o, nil

	case "backspace":
		if len(o.LayoutSaveBuffer) > 0 {
			o.LayoutSaveBuffer = o.LayoutSaveBuffer[:len(o.LayoutSaveBuffer)-1]
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
func handleLayoutLoadInput(keyStr string, o *app.OS) (*app.OS, tea.Cmd) {
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
			o.ShowNotification("Layout applied: "+selected.Name, "success", config.NotificationDuration)
		}
		return o, nil

	case "up", "ctrl+p":
		if o.LayoutPickerSelected > 0 {
			o.LayoutPickerSelected--
			if o.LayoutPickerSelected < o.LayoutPickerScroll {
				o.LayoutPickerScroll = o.LayoutPickerSelected
			}
		}
		return o, nil

	case "down", "ctrl+n":
		if o.LayoutPickerSelected < len(filtered)-1 {
			o.LayoutPickerSelected++
			maxVisible := 10
			if o.LayoutPickerSelected >= o.LayoutPickerScroll+maxVisible {
				o.LayoutPickerScroll = o.LayoutPickerSelected - maxVisible + 1
			}
		}
		return o, nil

	case "backspace":
		if len(o.LayoutPickerQuery) > 0 {
			o.LayoutPickerQuery = o.LayoutPickerQuery[:len(o.LayoutPickerQuery)-1]
			o.LayoutPickerSelected = 0
			o.LayoutPickerScroll = 0
		}
		return o, nil

	case "ctrl+u":
		o.LayoutPickerQuery = ""
		o.LayoutPickerSelected = 0
		o.LayoutPickerScroll = 0
		return o, nil

	default:
		// Delete action only when query is empty
		if o.LayoutPickerQuery == "" && keyStr == "d" {
			if len(filtered) > 0 && o.LayoutPickerSelected < len(filtered) {
				selected := filtered[o.LayoutPickerSelected]
				if err := app.DeleteLayoutTemplate(selected.Name); err != nil {
					o.ShowNotification("Failed to delete layout: "+err.Error(), "error", config.NotificationDuration)
				} else {
					o.ShowNotification("Layout deleted: "+selected.Name, "info", config.NotificationDuration)
					// Refresh the list
					templates, _ := app.LoadLayoutTemplates()
					o.LayoutPickerItems = templates
					if o.LayoutPickerSelected >= len(o.LayoutPickerItems) {
						o.LayoutPickerSelected = max(len(o.LayoutPickerItems)-1, 0)
					}
				}
			}
			return o, nil
		}

		// Accept printable characters for search
		if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
			o.LayoutPickerQuery += keyStr
			o.LayoutPickerSelected = 0
			o.LayoutPickerScroll = 0
		}
		return o, nil
	}
}
