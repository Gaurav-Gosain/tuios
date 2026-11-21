package app

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

func (m *OS) inboxDialer() inboxDial {
	host, build := m.AttachedHost, ""
	if m.DaemonClient != nil {
		build = m.DaemonClient.ClientVersion()
	}
	return func() (*session.VerbClient, error) {
		if host == "" {
			return session.DialVerbClientAs(build)
		}
		c, _, err := dialVerbThroughHost(host, build)
		return c, err
	}
}

func (m *OS) inboxActCaller() inboxVerbCall {
	if m.Inbox.call != nil || m.AttachedHost == "" {
		return m.inboxCaller()
	}
	dial := m.inboxDialer()
	return func(verb string, params map[string]any, timeout time.Duration) (json.RawMessage, error) {
		client, err := dial()
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()
		return client.CallWithTimeout(verb, params, timeout)
	}
}

func (m *OS) inboxCanAct(what string) bool {
	if m.DaemonClient != nil || m.Inbox.call != nil {
		return true
	}
	m.ShowNotification(what+" needs a client attached to a daemon", "warning", m.Settings.NotificationDuration)
	return false
}

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
