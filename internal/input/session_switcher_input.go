package input

import (
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
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

	if listKey(keyStr, false, listPage, o.SessionSwitcherMove) {
		return o, nil
	}

	switch keyStr {
	case "esc":
		o.ShowSessionSwitcher = false
		o.SessionSwitcherQuery = ""
		o.SessionSwitcherSelected = 0
		o.SessionSwitcherScroll = 0
		return o, nil

	case "enter":
		if selected, ok := o.SessionSwitcherTarget(o.SessionSwitcherSelected); ok {
			if selected.IsCurrent {
				o.ShowNotification("Already on this session", "info", o.Settings.NotificationDuration)
			} else {
				// The switch is made by the node, which knows the machine
				// and the name: Title is a label and may be a display name
				// that addresses nothing, and the ID of a row under another
				// machine is a rail identity that no daemon holds (#196).
				o.OpenSessionNode(selected)
			}
		} else if o.SessionSwitcherQuery != "" {
			// No matching session: create new one with the typed name
			if err := o.SwitchToSession(o.SessionSwitcherQuery); err != nil {
				o.ShowNotification("Create failed: "+err.Error(), "error", o.Settings.NotificationDuration*2)
			}
		}
		o.ShowSessionSwitcher = false
		o.SessionSwitcherQuery = ""
		o.SessionSwitcherSelected = 0
		o.SessionSwitcherScroll = 0
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
		// Renames the session through the daemon, so the new name is the
		// one ls lists and attach takes.
		if selected, ok := o.SessionSwitcherTarget(o.SessionSwitcherSelected); ok {
			if selected.Host != "" {
				// Renames go to the daemon this client is connected to,
				// which does not hold another machine's session.
				o.ShowNotification("Switch to "+selected.Host+" to rename this session", "info", o.Settings.NotificationDuration)
				return o, nil
			}
			o.BeginRenameSession(selected.ID)
		}
		return o, nil

	case "ctrl+d":
		// Request delete confirmation for the selected session
		if selected, ok := o.SessionSwitcherTarget(o.SessionSwitcherSelected); ok {
			if selected.Host != "" {
				// The kill goes to this client's daemon too, for the same reason.
				o.ShowNotification("Switch to "+selected.Host+" to delete this session", "info", o.Settings.NotificationDuration)
			} else if selected.IsCurrent {
				o.ShowNotification("Cannot delete the current session", "warning", o.Settings.NotificationDuration)
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
