package app

import (
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Which daemon the Inbox is.
//
// The Inbox a client shows, and every act of the person on it, belong to one
// daemon: the daemon of the session the client is attached to. That is this
// machine's daemon, or, on a client attached through a host (tuios attach
// --host), that host's daemon over the link. The attach nonce that signs the
// person's acts comes from that same daemon, so an act sent anywhere else
// would be refused. The far daemon holds each act to its link policy for
// this machine: dismissing, snoozing and answering need the respond grant
// there, and its refusal names the grant and the table to change.
//
// inboxDialer and inboxActCaller are the one place that choice is made. The
// watch (startInboxWatch) reads the queue through inboxDialer, and every act
// (dismiss-attention, mark-attention, reply-approval, answer-ask,
// peek-prompt, respond, release-agent-message, get-approval) goes through
// inboxActCaller.

// inboxDialer opens a verb connection to the daemon that holds the Inbox.
func (m *OS) inboxDialer() inboxDial { return m.attachedDialer() }

// inboxActCaller is how an act on the Inbox, or a read of an agent's pane,
// reaches the daemon that holds it. Tests set InboxState.call.
func (m *OS) inboxActCaller() inboxVerbCall { return m.machineCaller(m.attachedMachine()) }

// inboxCanAct reports whether this client has a daemon to send the person's
// acts to, and says why not when it has none.
func (m *OS) inboxCanAct(what string) bool {
	if m.DaemonClient != nil || m.Inbox.call != nil {
		return true
	}
	m.ShowNotification(what+" needs a client attached to a daemon", "warning", m.Settings.NotificationDuration)
	return false
}

// inboxElsewhere refuses an act on an item of another machine than the one
// whose Inbox this is, and says where it is. A client attached through a host
// signs with that host's nonce, so it cannot act on a third machine's item.
func (m *OS) inboxElsewhere(it session.AttentionItem, what string) bool {
	if it.Host == "" {
		return false
	}
	m.ShowNotification(what+" is not sent: this item is on "+printableTitle(it.Host)+". Attach there to act on it", "warning", m.Settings.NotificationDuration)
	return true
}

// InboxRewatchMsg starts the Inbox watch again, on the daemon the client is
// now attached to.
type InboxRewatchMsg struct{}

// restartInboxWatch drops the Inbox of the daemon the client left and
// follows the one it is attached to now.
func (m *OS) restartInboxWatch() tea.Cmd {
	if m.inboxEvents == nil {
		return m.startInboxWatch()
	}
	m.endInboxWatch()
	m.inboxEvents = nil
	m.Inbox.Items = nil
	m.Inbox.ck.cards = nil
	m.Inbox.Live = false
	m.MarkAllDirty()
	return m.startInboxWatch()
}

// inboxActErr is what the person reads when an act did not go through. A
// refusal of the link policy of host, the machine the Inbox is on, names the
// grant first, so the dock's line shows it, then the table on host that gives
// it.
func inboxActErr(err error, host string) string {
	ce, ok := errors.AsType[*session.VerbCallError](err)
	if !ok || ce.Code != session.ErrVerbForbidden || ce.Hint == nil || len(ce.Hint.Missing) == 0 || host == "" {
		return err.Error()
	}
	quoted := make([]string, 0, len(ce.Hint.Missing))
	for _, c := range ce.Hint.Missing {
		quoted = append(quoted, `"`+c+`"`)
	}
	grants := strings.Join(quoted, ", ")
	where := printableTitle(host)
	text := "it needs " + grants + " on " + where + "."
	if ce.Hint.Table != "" {
		text += " Add " + grants + " to allow in " + ce.Hint.Table + " in the config on " + where + "."
	}
	return text
}
