package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// inboxOS is a client attached to session "here", with two panes, a live
// Inbox and the given alert policy.
func inboxOS(t *testing.T, agent config.AgentAlertsConfig) *OS {
	t.Helper()
	m := alertOS(t, agent)
	m.SessionName = "here"
	m.IsDaemonSession = true
	m.Inbox.Live = true
	m.WorkspaceFocus = map[int]int{}
	return m
}

func item(id, kind, sess, window, summary string, since int64) session.AttentionItem {
	return session.AttentionItem{ID: id, Kind: kind, Session: sess, Window: window, Name: "agent-" + id, Summary: summary, Since: since}
}

func opened(items ...session.AttentionItem) InboxEventsMsg {
	var evs []InboxEvent
	for i := range items {
		evs = append(evs, InboxEvent{Action: session.AttentionOpened, Item: &items[i]})
	}
	return InboxEventsMsg{Events: evs}
}
