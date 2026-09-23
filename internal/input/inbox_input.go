package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleInboxInput handles keyboard input while the Inbox is open. Every
// action has a key: j and k move, enter goes to the item's pane (or opens its
// mail thread), d dismisses, r replies to mail, f steps the kind filter, m
// opens the whole mailbox, and esc or q closes.
func handleInboxInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		o.CloseInbox()
	case "enter":
		return o, o.InboxActivate()
	case "d", "delete":
		return o, o.InboxDismiss()
	case "r":
		return o, o.InboxReply()
	case "f":
		o.InboxCycleFilter()
	case "m":
		return o, o.InboxOpenMailbox()
	case "up", "k", "ctrl+p":
		o.InboxMove(-1)
	case "down", "j", "ctrl+n":
		o.InboxMove(1)
	case "pgup":
		o.InboxMove(-10)
	case "pgdown":
		o.InboxMove(10)
	case "home", "g":
		o.InboxMove(-1 << 20)
	case "end", "G":
		o.InboxMove(1 << 20)
	}
	return o, nil
}
