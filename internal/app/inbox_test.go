package app

import (
	"errors"
	"strings"
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

func TestInboxMirrorFollowsEvents(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{
		item("1", session.AttentionFinished, "a", "w1", "", 10),
		item("2", session.AttentionApproval, "a", "w2", "", 20),
	}})
	if m.Inbox.Items[0].ID != "2" {
		t.Fatalf("the snapshot is not in Inbox order: %+v", m.Inbox.Items)
	}
	gen := m.Inbox.Gen

	q := item("3", session.AttentionQuestion, "b", "w3", "which?", 5)
	m.applyInboxEvents(opened(q))
	closed := item("2", session.AttentionApproval, "a", "w2", "", 20)
	m.applyInboxEvents(InboxEventsMsg{Events: []InboxEvent{{Action: session.AttentionClosed, Item: &closed}}})
	var ids []string
	for _, it := range m.Inbox.Items {
		ids = append(ids, it.ID)
	}
	if got := strings.Join(ids, ","); got != "3,1" {
		t.Errorf("mirror holds %s, want 3,1", got)
	}
	if m.Inbox.Gen <= gen {
		t.Error("an event did not bump the generation the rail's cache keys on")
	}

	m.applyInboxDown(InboxDownMsg{})
	if m.Inbox.Live {
		t.Error("losing the daemon left the mirror marked live")
	}
}

// TestInboxAlertWaitsOutTheSettleWindow: an item that closes inside the window
// raises nothing, one that stays raises its alert when the window ends.
func TestInboxAlertWaitsOutTheSettleWindow(t *testing.T) {
	m := inboxOS(t, config.AgentAlertsConfig{})
	captureHost(t, m)
	flicker := item("1", session.AttentionApproval, "other", "w", "", 1)
	stays := item("2", session.AttentionErrored, "other", "v", "boom", 2)
	cmd := m.applyInboxEvents(opened(flicker, stays))
	if cmd == nil {
		t.Fatal("no settle timer was scheduled")
	}
	if len(m.Notifications) != 0 {
		t.Fatal("an alert fired before the settle window ended")
	}
	m.applyInboxEvents(InboxEventsMsg{Events: []InboxEvent{{Action: session.AttentionClosed, Item: &flicker}}})
	m.applyInboxAlertDue(InboxAlertDueMsg{IDs: []string{"1", "2"}})
	if len(m.Notifications) != 1 || !strings.Contains(m.Notifications[0].Message, "boom") {
		t.Fatalf("after the window: %+v, want one alert for the item that stayed", m.Notifications)
	}
}

// A dismiss the person did not ask for, and one that lost a race to another
// client or to the daemon's own close, show nothing. A dismiss the person
// asked for that really failed still says so.
func TestInboxDismissFailuresOnlyShowWhenTheyMatter(t *testing.T) {
	gone := &session.VerbCallError{
		Code:    session.ErrVerbInvalidParams,
		Message: "no open attention item has id 4",
		Hint:    &session.VerbHint{Param: "id"},
	}
	notHuman := &session.VerbCallError{
		Code:    session.ErrVerbNotHuman,
		Message: "dismiss-attention is for the person at an attached client",
		Hint:    &session.VerbHint{Param: "human_nonce"},
	}
	transport := errors.New("failed to send request: broken pipe")
	cases := []struct {
		name string
		msg  InboxDismissedMsg
		want int
	}{
		{"silent and already closed", InboxDismissedMsg{Err: gone, Silent: true}, 0},
		{"silent and broken", InboxDismissedMsg{Err: transport, Silent: true}, 0},
		{"asked for and already closed", InboxDismissedMsg{Err: gone}, 0},
		{"asked for and not human", InboxDismissedMsg{Err: notHuman}, 1},
		{"asked for and broken", InboxDismissedMsg{Err: transport}, 1},
		{"asked for and done", InboxDismissedMsg{}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := inboxOS(t, zeroSettle())
			m.applyInboxDismissed(c.msg)
			if len(m.Notifications) != c.want {
				t.Errorf("%d toasts, want %d", len(m.Notifications), c.want)
			}
		})
	}
}
