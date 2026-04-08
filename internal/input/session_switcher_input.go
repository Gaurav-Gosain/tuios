package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// handleSessionSwitcherInput handles keyboard input when the session switcher is open.
func handleSessionSwitcherInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	keyStr := msg.String()

	// Handle delete confirmation state first
	if o.SessionSwitcherConfirmDelete != "" {
		switch keyStr {
		case "y", "Y", "enter":
			name := o.SessionSwitcherConfirmDelete
			o.SessionSwitcherConfirmDelete = ""
			if o.DaemonClient != nil {
				if err := o.DaemonClient.KillSessionByName(name); err != nil {
					o.ShowNotification("Delete failed: "+err.Error(), "error", config.NotificationDuration*2)
				} else {
					o.ShowNotification("Deleted session: "+name, "success", config.NotificationDuration)
					o.SessionSwitcherItems = o.RefreshSessionList()
					if o.SessionSwitcherSelected >= len(o.SessionSwitcherItems) && o.SessionSwitcherSelected > 0 {
						o.SessionSwitcherSelected--
					}
				}
			}
			return o, nil
		case "n", "N", "esc":
			o.SessionSwitcherConfirmDelete = ""
			return o, nil
		}
		// Ignore all other keys while confirming
		return o, nil
	}

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
				if err := o.SwitchToSession(selected.Name); err != nil {
					o.ShowNotification("Switch failed: "+err.Error(), "error", config.NotificationDuration*2)
				}
			}
		} else if o.SessionSwitcherQuery != "" {
			// No matching session  - create new one with the typed name
			if err := o.SwitchToSession(o.SessionSwitcherQuery); err != nil {
				o.ShowNotification("Create failed: "+err.Error(), "error", config.NotificationDuration*2)
			}
		}
		o.ShowSessionSwitcher = false
		o.SessionSwitcherQuery = ""
		o.SessionSwitcherSelected = 0
		o.SessionSwitcherScroll = 0
		o.SessionSwitcherError = ""
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

	case "ctrl+d":
		// Request delete confirmation for the selected session
		if len(filtered) > 0 && o.SessionSwitcherSelected < len(filtered) {
			selected := filtered[o.SessionSwitcherSelected]
			if selected.IsCurrent {
				o.ShowNotification("Cannot delete the current session", "warning", config.NotificationDuration)
			} else {
				o.SessionSwitcherConfirmDelete = selected.Name
			}
		}
		return o, nil

	default:
		// Accept printable characters for fuzzy search (including 'n', 'd', etc.)
		if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
			o.SessionSwitcherQuery += keyStr
			o.SessionSwitcherSelected = 0
			o.SessionSwitcherScroll = 0
		}
		return o, nil
	}
}
