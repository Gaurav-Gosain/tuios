package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// TestInboxSaysMailWaitsForAMachine: mail queued for a machine whose link is
// down is a row under its own heading, naming the machine, and enter says what
// happens to it rather than going anywhere.
func TestInboxSaysMailWaitsForAMachine(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	out := session.AttentionItem{ID: "9", Kind: session.AttentionOutbox, ForHost: "build", Name: "build",
		Summary: "2 messages wait for the link to build", Count: 2, Since: time.Now().Add(-time.Minute).UnixNano()}
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{out}})
	m.OpenInbox("")
	view, _, _ := m.renderInbox()
	plain := ansi.Strip(view)
	for _, want := range []string{"Waiting to send", "for build", "2 messages wait for the link to build"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the Inbox does not say %q:\n%s", want, plain)
		}
	}
	if cmd := m.InboxActivate(); cmd != nil {
		t.Error("enter on an outbox item returned a command")
	}
	if !m.ShowInbox {
		t.Error("enter on an outbox item closed the Inbox")
	}
	if len(m.Notifications) == 0 || !strings.Contains(m.Notifications[len(m.Notifications)-1].Message, "when its link is back") {
		t.Errorf("enter on an outbox item said %+v", m.Notifications)
	}
	if inboxNeedsYou(out) {
		t.Error("mail waiting to be sent is visited by the next-attention key")
	}
}

// TestInboxReleaseOnlyAnswersHeldMail: p on anything else says what it is for
// and does nothing.
func TestInboxReleaseOnlyAnswersHeldMail(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{item("1", session.AttentionMail, "work", "", "hi", 1)}})
	m.OpenInbox("")
	if cmd := m.InboxRelease(); cmd != nil {
		t.Error("p on mail that is not held returned a command")
	}
	if n := len(m.Notifications); n == 0 || !strings.Contains(m.Notifications[n-1].Message, "p passes on") {
		t.Errorf("p on ordinary mail said %+v", m.Notifications)
	}
}
