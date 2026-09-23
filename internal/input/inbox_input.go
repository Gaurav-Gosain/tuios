package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// handleInboxInput handles keyboard input while the Inbox is open. Every
// action has a key: j and k move, space reads the prompt of an approval or a
// question, enter goes to the item's pane (or opens its mail thread), 1, 2
// and 3 answer an approval the Inbox is holding (allow once, always allow,
// deny, the order of the harness's own menu), 1 to 9 pick the answer to a
// question ask-human put to the person, d dismisses, r replies to mail,
// y resumes a conversation a restart left, p passes on mail another machine
// sent an agent here that the link policy held, f steps the kind filter, m opens
// the whole mailbox, and esc or q closes.
func handleInboxInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.InboxPeeking() {
		return handleInboxPeekInput(msg, o)
	}
	switch msg.String() {
	case "esc", "q":
		o.CloseInbox()
	case "space":
		return o, o.InboxPeek()
	case "enter":
		return o, o.InboxActivate()
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return o, o.InboxNumber(int(msg.String()[0] - '0'))
	case "d", "delete":
		return o, o.InboxDismiss()
	case "r":
		return o, o.InboxReply()
	case "y":
		return o, o.InboxResume()
	case "p":
		return o, o.InboxRelease()
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

// handleInboxPeekInput handles keyboard input while the peek is open over the
// Inbox. A digit chooses that option, a approves, A approves for good, d
// denies, tab opens the text line, r reads the prompt again, enter goes to the
// pane, and esc, q or space goes back to the list. While the text line is open
// every printable key is text, enter sends it, and esc drops it.
func handleInboxPeekInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := msg.String()
	if o.InboxPeekComposing() {
		switch key {
		case "esc":
			o.InboxPeekCancelText()
		case "enter":
			return o, o.InboxPeekSendText()
		case "backspace":
			o.InboxPeekBackspace()
		default:
			text := msg.Text
			if key == "space" {
				text = " "
			}
			o.InboxPeekType(text)
		}
		return o, nil
	}
	switch key {
	case "esc", "q", "space":
		o.InboxPeekBack()
	case "enter":
		o.InboxPeekGo()
	case "a":
		return o, o.InboxAnswer(harness.ActionApprove, "")
	case "A", "shift+a":
		return o, o.InboxAnswer(harness.ActionApproveAlways, "")
	case "d":
		return o, o.InboxAnswer(harness.ActionDeny, "")
	case "tab":
		o.InboxPeekStartText()
	case "r":
		return o, o.InboxPeekRefresh()
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return o, o.InboxAnswer(harness.ActionChoose, key)
	}
	return o, nil
}
