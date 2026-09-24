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
// types a selector that narrows the list, m opens the whole mailbox, z
// snoozes (then 1 to 4 for how long) or wakes a snoozed item, u undoes the
// last dismiss or snooze, S shows the snoozed items, and esc or q closes.
func handleInboxInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// A question that opened the Inbox by itself a moment ago: the key was
	// most likely typed for the pane, so it does nothing here.
	if o.InboxPopSettling() {
		return o, nil
	}
	// A risky allow's first press waits for the same key again; any other
	// key resets it.
	o.InboxKeyPressed(msg.String())
	if o.InboxPeeking() {
		return handleInboxPeekInput(msg, o)
	}
	if o.InboxReplyOpen() {
		return handleInboxReplyInput(msg, o)
	}
	if o.InboxReasonOpen() {
		return handleInboxReasonInput(msg, o)
	}
	if o.InboxSelecting() {
		return handleInboxSelectInput(msg, o)
	}
	// The snooze picker takes the next key: a digit from 1 to 4 is how long,
	// anything else closes it.
	if o.InboxSnoozePicking() {
		return o, o.InboxSnoozeKey(msg.String())
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
	// The review, triage and approval keys say whether they did anything,
	// and one that did not is not recorded as run: until its work lands it
	// does what an unbound key did.
	if app.InboxWorkActions[action] {
		cmd, handled := o.InboxWorkAction(action)
		if handled {
			o.NoteAction(action)
		}
		return o, cmd
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

// handleInboxReasonInput handles keyboard input while the reason line for a
// deny is open: every printable key is text, enter denies with it, backspace
// deletes, and esc closes the line and denies nothing.
func handleInboxReasonInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch key := msg.String(); key {
	case "esc":
		o.InboxReasonCancel()
	case "enter":
		return o, o.InboxReasonSend()
	case "backspace":
		o.InboxReasonBackspace()
	case "space":
		o.InboxReasonType(" ")
	default:
		o.InboxReasonType(msg.Text)
	}
	return o, nil
}

// handleInboxReplyInput handles keyboard input while the reply editor is
// open: every printable key is text, enter queues the reply, backspace
// deletes, and esc closes the editor and sends nothing.
func handleInboxReplyInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch key := msg.String(); key {
	case "esc":
		o.InboxReplyCancel()
	case "enter":
		return o, o.InboxReplySend()
	case "backspace":
		o.InboxReplyBackspace()
	case "space":
		o.InboxReplyType(" ")
	default:
		o.InboxReplyType(msg.Text)
	}
	return o, nil
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
