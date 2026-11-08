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
// and 1 2 3 ask to answer a held approval (allow once, always allow,
// reject), and enter then answers it (R rejects and says why), 1 to 9 pick the answer to a question ask-human put
// to the person, r or i replies to an agent or to mail, tab writes a reply,
// n starts a new agent, d dismisses, y resumes a conversation a restart
// left, p passes on held mail, f steps the filter, / searches, m opens the
// whole mailbox, z snoozes (then 1 to 4 for how long) or wakes a snoozed
// item, u undoes the last dismiss or snooze, S shows the snoozed items, x
// marks an agent or a resume offer for a bulk answer (with marks, y resumes
// every marked offer), ? opens help, and esc or q closes. The
// digits are the numbers the prompt shows, so they are not bindings.
func handleInboxInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// A question that opened the Inbox by itself a moment ago: the key was
	// most likely typed for the pane, so it does nothing here.
	if o.InboxPopSettling() {
		return o, nil
	}
	// The layer on top takes the key: one decision, in app.CockpitTop, that
	// the key bar and the clicks read too.
	top := o.CockpitTop()
	// Copy mode over the thread takes every key, as it does over a pane.
	if top == app.CockpitLayerCopy {
		return handleCockpitCopyKey(msg, o)
	}
	// The global keys (the palette, the launcher) act over the view and
	// over every sheet and field in it, and open on top of it. A plain
	// character stays the view's: it is a list key, or text in a field. The
	// reply's @ list keeps ctrl+p and ctrl+n, its up and down.
	if !plainTextKey(msg) && !(o.InboxMentionListOpen() && listMoveKey(msg)) {
		if out, cmd, ok := handleGlobalBinds(msg, o); ok {
			return out, cmd
		}
	}
	switch top {
	case app.CockpitLayerImage:
		// The image view takes every key while it is up.
		return o, o.CockpitImageKey(msg.String())
	case app.CockpitLayerSpawn:
		// The spawn sheet takes every key while it is up.
		return o, o.CockpitSpawnKey(msg)
	case app.CockpitLayerCompare:
		// The comparison of a fan's attempts takes every key while it is up.
		return o, o.CockpitCompareKey(msg.String())
	case app.CockpitLayerArtifact:
		// A TML surface drawn in the view takes every key while the person
		// is in it; esc it does not use gives them back.
		return o, o.CockpitArtifactKey(msg.String())
	case app.CockpitLayerNewCard:
		// The line for a new card on the board takes every key while it is
		// open.
		return o, o.CockpitNewCardKey(msg)
	case app.CockpitLayerConfirm:
		// An answer key only asks: enter sends the answer, and any other key
		// drops it and does nothing else.
		return o, o.InboxConfirmKey(msg.String())
	}
	// A risky allow's first press in the peek waits for the same key again;
	// any other key resets it.
	o.InboxKeyPressed(msg.String())
	switch top {
	case app.CockpitLayerPeek:
		return handleInboxPeekInput(msg, o)
	case app.CockpitLayerReply:
		return handleInboxReplyInput(msg, o)
	case app.CockpitLayerReason:
		return handleInboxReasonInput(msg, o)
	case app.CockpitLayerSelect:
		return handleInboxSelectInput(msg, o)
	case app.CockpitLayerGroupMenu:
		// The group and sort picker takes the next key: a digit chooses the
		// grouping, s steps the sort, anything else closes it.
		return o, o.CockpitGroupMenuKey(msg.String())
	case app.CockpitLayerShip:
		// Ready to merge takes the next key: c, p or o, enter to send,
		// anything else closes it.
		return o, o.CockpitShipKey(msg.String())
	case app.CockpitLayerSnooze:
		// The snooze picker takes the next key: a digit from 1 to 4 is how
		// long, anything else closes it.
		return o, o.InboxSnoozeKey(msg.String())
	case app.CockpitLayerBulkSheet:
		// The bulk allow's sheet takes the next key: enter allows, anything
		// else closes it.
		return o, o.InboxAgentsSheetKey(msg.String())
	}
	key := msg.String()
	marks := o.InboxAgentsMarks() > 0
	action := tableAction(o, msg, overlayKeys(o), config.TableInbox)
	switch action {
	case config.ActionInboxMark:
		noteInboxAction(o, action)
		o.InboxAgentsToggleMark()
		return o, nil
	case config.ActionInboxMarkAll:
		noteInboxAction(o, action)
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
		noteInboxAction(o, action)
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
			noteInboxAction(o, action)
		}
		return o, cmd
	}
	noteInboxAction(o, action)
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
		if marks {
			return o, o.InboxAgentsBulkResume()
		}
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
	action := ""
	if o.InboxAgentsOn() {
		action = tableAction(o, msg, overlayKeys(o), config.TableInbox)
	}
	if action == config.ActionInboxViewPane {
		o.InboxReplyCancel()
		return o, o.InboxAgentsViewPane()
	}
	// tab and shift+tab step out of the reply to the next region and keep
	// the draft. In the @ list tab puts the entry in.
	if (action == config.ActionInboxRegionNext || action == config.ActionInboxRegionPrev) && !o.InboxMentionListOpen() {
		return o, o.CockpitKeyAction(action)
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
	// A key that answers the prompt only asks: enter answers, and any other
	// key drops it and does nothing else.
	if o.InboxPeekAskOpen() {
		return o, o.InboxPeekAskKey(key)
	}
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		o.InboxPeekAsk(harness.ActionChoose, key)
		return o, nil
	}
	action := tableAction(o, msg, overlayKeys(o), config.TableInboxPeek)
	if action == "" {
		return o, nil
	}
	noteInboxAction(o, action)
	switch action {
	case config.ActionPeekBack:
		o.InboxPeekBack()
	case config.ActionPeekGo:
		o.InboxPeekGo()
	case config.ActionPeekApprove:
		o.InboxPeekAsk(harness.ActionApprove, "")
	case config.ActionPeekApproveAlways:
		o.InboxPeekAsk(harness.ActionApproveAlways, "")
	case config.ActionPeekDeny:
		o.InboxPeekAsk(harness.ActionDeny, "")
	case config.ActionPeekType:
		o.InboxPeekStartText()
	case config.ActionPeekReadAgain:
		return o, o.InboxPeekRefresh()
	}
	return o, nil
}

// noteInboxAction records an action of the agents view as run: for a crash
// report, and for the browser build's tour, which hears it as it hears a
// keybinding's.
func noteInboxAction(o *app.OS, action string) {
	o.NoteAction(action)
	if o.OnAction != nil {
		o.OnAction(action)
	}
}

// plainTextKey reports whether the key types a character with no ctrl, alt
// or super held: a letter, a digit, a mark or space.
func plainTextKey(msg tea.KeyPressMsg) bool {
	return msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper|tea.ModHyper|tea.ModMeta) == 0
}

// listMoveKey reports whether the key is ctrl+p or ctrl+n, the up and down of
// a pop-up list.
func listMoveKey(msg tea.KeyPressMsg) bool {
	return msg.Mod&tea.ModCtrl != 0 && (msg.Code == 'p' || msg.Code == 'n')
}
