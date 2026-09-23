package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// heldApproval is an approval item the Inbox is holding for an answer.
func heldApproval(id, requestID string, options ...string) session.AttentionItem {
	it := item(id, session.AttentionApproval, "fan-2", "w-"+id, "approve Bash: go test", time.Now().UnixNano())
	it.RequestID = requestID
	it.Options = options
	it.Expires = time.Now().Add(time.Minute).UnixNano()
	return it
}

// TestInboxHeldApprovalSaysHowToAnswer: a held approval says in text which
// keys answer it, the hint bar names each one, and an approval that is not
// held shows neither.
func TestInboxHeldApprovalSaysHowToAnswer(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{
		heldApproval("1", "r1", session.ApprovalOnce, session.ApprovalDeny),
		item("2", session.AttentionApproval, "work", "w-2", "approve Edit: main.go", time.Now().UnixNano()),
	}})
	m.OpenInbox("")
	out, _, _ := m.renderInbox()
	plain := ansi.Strip(out)
	for _, want := range []string{"[1/3] approve Bash: go test", "allow", "deny", "answer in pane"} {
		if !strings.Contains(plain, want) {
			t.Errorf("a held approval does not show %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "always") || strings.Contains(plain, "[1/2/3]") {
		t.Errorf("the Inbox offers always, which this prompt does not:\n%s", plain)
	}
	if strings.Contains(plain, "] approve Edit") {
		t.Errorf("an approval the Inbox is not holding shows answer keys:\n%s", plain)
	}

	m.InboxMove(1)
	out, _, _ = m.renderInbox()
	if plain := ansi.Strip(out); strings.Contains(plain, "answer in pane") || !strings.Contains(plain, "dismiss") {
		t.Errorf("an approval that is not held shows the answer hints:\n%s", plain)
	}
}

// TestInboxAnswerOnlySendsWhatThePromptTakes: a key that does not answer the
// selected item says so and sends nothing.
func TestInboxReplyApprovalOnlySendsWhatThePromptTakes(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{
		heldApproval("1", "r1", session.ApprovalOnce, session.ApprovalDeny),
		item("2", session.AttentionErrored, "work", "w-2", "boom", 1),
	}})
	m.OpenInbox("")
	if cmd := m.InboxReplyApproval(session.ApprovalAlways); cmd != nil {
		t.Fatal("always was sent for a prompt that does not offer it")
	}
	if n := len(m.Notifications); n != 1 || !strings.Contains(m.Notifications[0].Message, "does not offer always") {
		t.Fatalf("notifications %+v", m.Notifications)
	}

	m.InboxMove(1)
	if cmd := m.InboxReplyApproval(session.ApprovalOnce); cmd != nil {
		t.Fatal("an errored item was answered")
	}
	if !strings.Contains(m.Notifications[len(m.Notifications)-1].Message, "answer an approval the Inbox is holding") {
		t.Errorf("notifications %+v", m.Notifications)
	}

	// With no daemon connection there is no nonce, so nothing can be sent
	// as the person.
	m.InboxMove(-1)
	if cmd := m.InboxReplyApproval(session.ApprovalOnce); cmd != nil {
		t.Fatal("an answer was sent with no daemon client to vouch for it")
	}
}

// TestInboxSaysWhenAnotherClientAnswered: two people, two screens, one
// approval. The one who did not answer hears who did and what.
func TestInboxSaysWhenAnotherClientAnswered(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	held := heldApproval("1", "r1", session.ApprovalOnce, session.ApprovalDeny)
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{held}})
	closed := held
	closed.Closed, closed.Answer, closed.AnsweredBy = session.AttentionClosedAnswered, session.ApprovalDeny, "client-9"
	m.applyInboxEvents(InboxEventsMsg{Events: []InboxEvent{{Action: session.AttentionClosed, Item: &closed}}})
	if len(m.Inbox.Items) != 0 {
		t.Fatal("the answered item is still listed")
	}
	if n := len(m.Notifications); n != 1 || !strings.Contains(m.Notifications[0].Message, "answered from another client: denied") {
		t.Fatalf("notifications %+v", m.Notifications)
	}

	// This client's own answer is not news to it.
	m = inboxOS(t, zeroSettle())
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{held}})
	m.Inbox.replied = map[string]bool{"r1": true}
	m.applyInboxEvents(InboxEventsMsg{Events: []InboxEvent{{Action: session.AttentionClosed, Item: &closed}}})
	if len(m.Notifications) != 0 {
		t.Errorf("its own answer was announced: %+v", m.Notifications)
	}
}

func TestInboxSaysWhatBecameOfAnAnswer(t *testing.T) {
	for _, tc := range []struct {
		msg  InboxApprovalRepliedMsg
		want string
	}{
		{InboxApprovalRepliedMsg{Name: "api", Decision: session.ApprovalOnce, Standing: session.ApprovalOnce, Applied: true}, "api: allowed once"},
		{InboxApprovalRepliedMsg{Name: "api", Decision: session.ApprovalDeny, Standing: session.ApprovalAlways}, "api was already answered: always allowed"},
		{InboxApprovalRepliedMsg{Name: "api", Decision: session.ApprovalOnce, Err: errors.New("hold ended")}, "Answer in the pane"},
		{InboxApprovalRepliedMsg{Name: "api", Decision: session.ApprovalAsk, Applied: true}, ""},
	} {
		m := inboxOS(t, zeroSettle())
		m.applyInboxApprovalReplied(tc.msg)
		switch {
		case tc.want == "" && len(m.Notifications) != 0:
			t.Errorf("%+v said %q", tc.msg, m.Notifications[0].Message)
		case tc.want != "" && (len(m.Notifications) != 1 || !strings.Contains(m.Notifications[0].Message, tc.want)):
			t.Errorf("%+v said %+v, want %q", tc.msg, m.Notifications, tc.want)
		}
	}
}
