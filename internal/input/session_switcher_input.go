package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// handleSessionSwitcherInput handles keyboard input when the session switcher is open.
func handleSessionSwitcherInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	keyStr := msg.String()

	filtered := app.FilterSessionItems(o.SessionSwitcherItems, o.SessionSwitcherQuery)

	switch keyStr {
	case "esc":
		o.ShowSessionSwitcher = false
		o.SessionSwitcherQuery = ""
		o.SessionSwitcherSelected = 0
		o.SessionSwitcherScroll = 0
		o.SessionSwitcherError = ""
		return o, nil

	case "enter":
		if len(filtered) > 0 && o.SessionSwitcherSelected < len(filtered) {
			selected := filtered[o.SessionSwitcherSelected]
			if selected.IsCurrent {
				o.ShowNotification("Already on this session", "info", config.NotificationDuration)
			} else {
				o.ShowNotification("Session switching is not yet supported in-app. Detach and reattach with: tuios attach "+selected.Name, "info", config.NotificationDuration*2)
			}
			o.ShowSessionSwitcher = false
			o.SessionSwitcherQuery = ""
			o.SessionSwitcherSelected = 0
			o.SessionSwitcherScroll = 0
			o.SessionSwitcherError = ""
		}
		return o, nil

	case "up", "ctrl+p":
		if o.SessionSwitcherSelected > 0 {
			o.SessionSwitcherSelected--
			if o.SessionSwitcherSelected < o.SessionSwitcherScroll {
				o.SessionSwitcherScroll = o.SessionSwitcherSelected
			}
		}
		return o, nil

	case "down", "ctrl+n":
		if o.SessionSwitcherSelected < len(filtered)-1 {
			o.SessionSwitcherSelected++
			maxVisible := 10
			if o.SessionSwitcherSelected >= o.SessionSwitcherScroll+maxVisible {
				o.SessionSwitcherScroll = o.SessionSwitcherSelected - maxVisible + 1
			}
		}
		return o, nil

	case "backspace":
		if len(o.SessionSwitcherQuery) > 0 {
			o.SessionSwitcherQuery = o.SessionSwitcherQuery[:len(o.SessionSwitcherQuery)-1]
			o.SessionSwitcherSelected = 0
			o.SessionSwitcherScroll = 0
		}
		return o, nil

	case "ctrl+u":
		o.SessionSwitcherQuery = ""
		o.SessionSwitcherSelected = 0
		o.SessionSwitcherScroll = 0
		return o, nil

	default:
		// Handle special action keys only when query is empty (to avoid conflicts with typing)
		if o.SessionSwitcherQuery == "" {
			switch keyStr {
			case "n":
				o.ShowNotification("Use `tuios new <name>` to create a session", "info", config.NotificationDuration*2)
				o.ShowSessionSwitcher = false
				o.SessionSwitcherQuery = ""
				o.SessionSwitcherSelected = 0
				o.SessionSwitcherScroll = 0
				o.SessionSwitcherError = ""
				return o, nil
			case "d":
				if len(filtered) > 0 && o.SessionSwitcherSelected < len(filtered) {
					selected := filtered[o.SessionSwitcherSelected]
					if selected.IsCurrent {
						o.ShowNotification("Cannot delete the current session", "warning", config.NotificationDuration)
					} else {
						o.ShowNotification("Use `tuios kill "+selected.Name+"` to delete this session", "info", config.NotificationDuration*2)
					}
				}
				return o, nil
			}
		}

		// Accept printable characters for search
		if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
			o.SessionSwitcherQuery += keyStr
			o.SessionSwitcherSelected = 0
			o.SessionSwitcherScroll = 0
		}
		return o, nil
	}
}
