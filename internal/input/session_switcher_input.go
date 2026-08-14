package input

import (
	"unicode/utf8"

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
			// The identity, which is what the row carried and what the daemon
			// answers to. The kill is a round trip that waits for the post-kill
			// listing, so it runs off this goroutine and reports when it lands,
			// which is also what refreshes the rows behind this dialog.
			name := o.SessionSwitcherConfirmDelete
			o.SessionSwitcherConfirmDelete = ""
			o.KillOtherSession(name)
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
		if selected, ok := o.SessionSwitcherTarget(o.SessionSwitcherSelected); ok {
			if selected.IsCurrent {
				o.ShowNotification("Already on this session", "info", config.NotificationDuration)
			} else {
				// The switch is made by identity: Title is a label and may be
				// a display name that addresses nothing.
				if err := o.SwitchToSession(selected.ID); err != nil {
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
		if o.SessionSwitcherQuery != "" {
			_, size := utf8.DecodeLastRuneInString(o.SessionSwitcherQuery)
			o.SessionSwitcherQuery = o.SessionSwitcherQuery[:len(o.SessionSwitcherQuery)-size]
			o.SessionSwitcherSelected = 0
			o.SessionSwitcherScroll = 0
		}
		return o, nil

	case "ctrl+u":
		o.SessionSwitcherQuery = ""
		o.SessionSwitcherSelected = 0
		o.SessionSwitcherScroll = 0
		return o, nil

	case "ctrl+r":
		// Renames the label, never the identity: the session keeps the name it
		// is addressed, persisted and detached by.
		if selected, ok := o.SessionSwitcherTarget(o.SessionSwitcherSelected); ok {
			o.BeginRenameSession(selected.ID)
		}
		return o, nil

	case "ctrl+d":
		// Request delete confirmation for the selected session
		if selected, ok := o.SessionSwitcherTarget(o.SessionSwitcherSelected); ok {
			if selected.IsCurrent {
				o.ShowNotification("Cannot delete the current session", "warning", config.NotificationDuration)
			} else {
				o.SessionSwitcherConfirmDelete = selected.ID
			}
		}
		return o, nil

	default:
		// The search has to be able to spell the names the rename editor can now
		// produce, so it takes a space and any typed rune the same way the
		// palette does. A session called "Payments API" is otherwise unfindable
		// past its first word.
		text := msg.Text
		if keyStr == "space" {
			text = " "
		}
		if text != "" {
			o.SessionSwitcherQuery += text
			o.SessionSwitcherSelected = 0
			o.SessionSwitcherScroll = 0
		}
		return o, nil
	}
}
