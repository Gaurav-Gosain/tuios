package input

import (
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// handleInboxInput handles keyboard input while the agents view is open.
// Every action is a binding in [keybindings.inbox], and these are the
// defaults: j and k move, space reads the prompt of an approval or a
// question, enter goes to the agent's pane (or opens its mail thread), a A r
// R and 1 2 3 answer a held approval (allow once, always allow, reject; R
// rejects and says why), 1 to 9 pick the answer to a question ask-human put
// to the person, r or i replies to an agent or to mail, tab writes a reply,
// n starts a new agent, d dismisses, y resumes a conversation a restart
// left, p passes on held mail, f steps the filter, / searches, m opens the
// whole mailbox, z snoozes (then 1 to 4 for how long) or wakes a snoozed
// item, u undoes the last dismiss or snooze, S shows the snoozed items, x
// marks an agent for a bulk answer, ? opens help, and esc or q closes. The
// digits are the numbers the prompt shows, so they are not bindings.
func handleInboxInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// A question that opened the Inbox by itself a moment ago: the key was
	// most likely typed for the pane, so it does nothing here.
	if o.InboxPopSettling() {
		return o, nil
	}
	// The image view takes every key while it is up.
	if o.CockpitImageOpen() {
		return o, o.CockpitImageKey(msg.String())
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
	// The bulk allow's sheet takes the next key: enter allows, anything
	// else closes it.
	if o.InboxAgentsSheetOpen() {
		return o, o.InboxAgentsSheetKey(msg.String())
	}
	key := msg.String()
	marks := o.InboxAgentsMarks() > 0
	action := lookupAction(o, msg, overlayKeys(o).GetInboxAction)
	switch action {
	case config.ActionInboxMark:
		o.NoteAction(action)
		o.InboxAgentsToggleMark()
		return o, nil
	case config.ActionInboxMarkAll:
		o.NoteAction(action)
		o.InboxAgentsMarkAll()
		return o, nil
	}
	// A digit answers by its number, which is the number the prompt shows, so
	// it is not a binding. With marks, 1 opens the bulk allow's sheet and 3
	// denies every marked approval.
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		if marks {
			switch key {
			case "1":
				o.InboxAgentsBulkAllow()
			case "3":
				return o, o.InboxAgentsBulkDeny()
			}
			return o, nil
		}
		o.CockpitSync()
		return o, o.InboxNumber(int(key[0] - '0'))
	}
	if marks && action == config.ActionInboxReply {
		return o, o.InboxAgentsBulkReply()
	}
	// The cockpit's own actions: a A r on an approval, n, tab, the moves.
	if cmd, handled := o.CockpitKey(action); handled {
		o.NoteAction(action)
		return o, cmd
	}
	if action == "" {
		return o, nil
	}
	o.CockpitSync()
	if marks && action == config.ActionInboxSnooze {
		o.InboxAgentsBulkSnooze()
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
	case config.ActionInboxDismiss:
		if marks {
			return o, o.InboxAgentsBulkDismiss()
		}
		return o, o.InboxDismiss()
	case config.ActionInboxResume:
		return o, o.InboxResume()
	case config.ActionInboxPassOn:
		return o, o.InboxRelease()
	case config.ActionInboxMailbox:
		return o, o.InboxOpenMailbox()
	case config.ActionInboxSort:
		o.InboxAgentsCycleSort()
		o.CockpitTouch()
	case config.ActionInboxViewPane:
		return o, o.InboxAgentsViewPane()
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
// deny is open. The line is a one-line composer: enter denies with it, esc
// closes it and denies nothing, and every other key edits it.
func handleInboxReasonInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.InboxReasonKey(msg)
}

// handleInboxReplyInput handles keyboard input while the reply editor is
// open. The composer takes every key: enter queues the reply, ctrl+j makes a
// new line, and esc closes the editor and keeps the draft. See
// internal/composer for the rest.
func handleInboxReplyInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// In the agents view the key that views the pane works while the editor
	// is open: the draft is kept, as esc keeps it. The composer binds no
	// ctrl+] of its own.
	if o.InboxAgentsOn() && lookupAction(o, msg, overlayKeys(o).GetInboxAction) == config.ActionInboxViewPane {
		o.InboxReplyCancel()
		return o, o.InboxAgentsViewPane()
	}
	return o, o.InboxReplyKey(msg)
}

// handleInboxSelectInput handles keyboard input while the selector line is
// open. The line is a one-line composer: enter applies the selector (an empty
// line clears it), esc closes the line and keeps the selector in force, and
// every other key edits it.
func handleInboxSelectInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.InboxSelectKey(msg)
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
		return o, o.InboxPeekTextKey(msg)
	}
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		return o, o.InboxAnswer(harness.ActionChoose, key)
	}
	action := lookupAction(o, msg, overlayKeys(o).GetInboxPeekAction)
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
