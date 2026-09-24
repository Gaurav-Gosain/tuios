package input

import (
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// handleInboxInput handles keyboard input while the Inbox is open. Every
// action has a key: j and k move, space reads the prompt of an approval or a
// question, enter goes to the item's pane (or opens its mail thread), 1, 2
// and 3 answer an approval the Inbox is holding (allow once, always allow,
// deny, the order of the harness's own menu), 1 to 9 pick the answer to a
// question ask-human put to the person, d dismisses, r replies to mail,
// y resumes a conversation a restart left, p passes on mail another machine
// sent an agent here that the link policy held, f steps the kind filter, /
// types a selector that narrows the list, m opens the whole mailbox, and esc
// or q closes.
func handleInboxInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// A question that opened the Inbox by itself a moment ago: the key was
	// most likely typed for the pane, so it does nothing here.
	if o.InboxPopSettling() {
		return o, nil
	}
	if o.InboxPeeking() {
		return handleInboxPeekInput(msg, o)
	}
	if o.InboxSelecting() {
		return handleInboxSelectInput(msg, o)
	}
	// A digit answers by its number, which is the number the prompt shows, so
	// it is not a binding.
	if key := msg.String(); len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		return o, o.InboxNumber(int(key[0] - '0'))
	}
	action := lookupAction(msg, overlayKeys(o).GetInboxAction)
	if action == "" {
		return o, nil
	}
	o.NoteAction(action)
	switch action {
	case config.ActionInboxSelect:
		o.InboxStartSelect()
	case config.ActionInboxClose:
		o.CloseInbox()
	case config.ActionInboxPeek:
		return o, o.InboxPeek()
	case config.ActionInboxGo:
		return o, o.InboxActivate()
	case config.ActionInboxDismiss:
		return o, o.InboxDismiss()
	case config.ActionInboxReply:
		return o, o.InboxReply()
	case config.ActionInboxResume:
		return o, o.InboxResume()
	case config.ActionInboxPassOn:
		return o, o.InboxRelease()
	case config.ActionInboxFilter:
		o.InboxCycleFilter()
	case config.ActionInboxMailbox:
		return o, o.InboxOpenMailbox()
	case config.ActionInboxUp:
		o.InboxMove(-1)
	case config.ActionInboxDown:
		o.InboxMove(1)
	case config.ActionInboxPageUp:
		o.InboxMove(-10)
	case config.ActionInboxPageDown:
		o.InboxMove(10)
	case config.ActionInboxFirst:
		o.InboxMove(-1 << 20)
	case config.ActionInboxLast:
		o.InboxMove(1 << 20)
	}
	return o, nil
}

// defaultOverlayKeys is the registry of the shipped bindings, for a client
// built without one. The Inbox and the mailbox own the keyboard while they are
// up, so a client with no registry must still be able to leave them.
var defaultOverlayKeys = sync.OnceValue(func() *config.KeybindRegistry {
	return config.NewKeybindRegistry(config.DefaultConfig())
})

// overlayKeys is the registry the Inbox, its peek and the mailbox read their
// keys from.
func overlayKeys(o *app.OS) *config.KeybindRegistry {
	if o.KeybindRegistry != nil {
		return o.KeybindRegistry
	}
	return defaultOverlayKeys()
}

// handleInboxSelectInput handles keyboard input while the selector line is
// open: every printable key is text, enter applies the selector (an empty line
// clears it), backspace deletes, and esc closes the line and keeps the
// selector in force.
func handleInboxSelectInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch key := msg.String(); key {
	case "esc":
		o.InboxSelectCancel()
	case "enter":
		o.InboxSelectApply()
	case "backspace":
		o.InboxSelectBackspace()
	case "space":
		o.InboxSelectType(" ")
	default:
		o.InboxSelectType(msg.Text)
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
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		return o, o.InboxAnswer(harness.ActionChoose, key)
	}
	action := lookupAction(msg, overlayKeys(o).GetInboxPeekAction)
	if action == "" {
		return o, nil
	}
	o.NoteAction(action)
	switch action {
	case config.ActionPeekBack:
		o.InboxPeekBack()
	case config.ActionPeekGo:
		o.InboxPeekGo()
	case config.ActionPeekApprove:
		return o, o.InboxAnswer(harness.ActionApprove, "")
	case config.ActionPeekApproveAlways:
		return o, o.InboxAnswer(harness.ActionApproveAlways, "")
	case config.ActionPeekDeny:
		return o, o.InboxAnswer(harness.ActionDeny, "")
	case config.ActionPeekType:
		o.InboxPeekStartText()
	case config.ActionPeekReadAgain:
		return o, o.InboxPeekRefresh()
	}
	return o, nil
}
