package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// TestInboxSaysMailIsHeldInWords: mail another machine sent an agent here,
// held by the link policy, reads as held, for whom, and with the key that
// passes it on, without relying on colour.
func TestInboxSaysMailIsHeldInWords(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	held := item("1", session.AttentionMail, "work", "", "run the migration", time.Now().Add(-time.Minute).UnixNano())
	held.HeldID, held.HeldFor = 12, "api"
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{held}})
	m.OpenInbox("")
	out, _, _ := m.renderInbox()
	plain := ansi.Strip(out)
	for _, want := range []string{"held for api", "p passes on", "pass on"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the Inbox does not say %q:\n%s", want, plain)
		}
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
