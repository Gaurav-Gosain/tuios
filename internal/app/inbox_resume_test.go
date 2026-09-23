package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// TestInboxAnnouncesResumesOnce: a restore happens before anyone attaches, so
// the resume items arrive in the first listing, where no event announces
// them. The client says so once, with the key that answers, and not again on
// the next listing.
func TestInboxAnnouncesResumesOnce(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	resume := item("7", session.AttentionResume, "here", "w-2", "claude --resume 5f1c", 1)
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{resume}})
	if len(m.Notifications) != 1 {
		t.Fatalf("a resume item in the first listing raised %d dock messages, want 1", len(m.Notifications))
	}
	msg := m.Notifications[0].Message
	for _, want := range []string{"agent-7", "can resume its conversation", "press y"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the announcement %q does not say %q", msg, want)
		}
	}

	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{resume}})
	if len(m.Notifications) != 1 {
		t.Errorf("a second listing announced the same item again: %d messages", len(m.Notifications))
	}

	// A burst from a restore says how many.
	m.applyInboxEvents(opened(
		item("8", session.AttentionResume, "fan", "w1", "codex resume 1", 2),
		item("9", session.AttentionResume, "fan", "w2", "codex resume 2", 3),
	))
	if len(m.Notifications) != 2 || !strings.Contains(m.Notifications[1].Message, "2 agent conversations can be resumed") {
		t.Errorf("a burst was announced as %+v", m.Notifications)
	}
}

// TestInboxRendersResumeWithItsKey: the group is named in words, the row
// shows the exact command, and the footer names y while a resume is selected.
func TestInboxRendersResumeWithItsKey(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	now := time.Now()
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{
		item("1", session.AttentionResume, "work", "w1", "claude --resume 5f1c", now.Add(-time.Minute).UnixNano()),
	}})
	m.OpenInbox("")
	out, _, _ := m.renderInbox()
	plain := ansi.Strip(out)
	for _, want := range []string{"Resume 1", "claude --resume 5f1c", "resume"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the Inbox does not show %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "reply") {
		t.Errorf("the footer offers reply on a resume item:\n%s", plain)
	}
}

// TestInboxResumeOnlyAnswersResumeItems: y on anything else says what it is
// for and does nothing.
func TestInboxResumeOnlyAnswersResumeItems(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{item("1", session.AttentionApproval, "here", "w-2", "", 1)}})
	m.OpenInbox("")
	if cmd := m.InboxResume(); cmd != nil {
		t.Error("y on an approval returned a command")
	}
	if !m.ShowInbox {
		t.Error("y on an approval closed the Inbox")
	}
	if n := len(m.Notifications); n == 0 || !strings.Contains(m.Notifications[n-1].Message, "y resumes") {
		t.Errorf("y on an approval said %+v", m.Notifications)
	}
}

// TestInboxResumedSaysWhatHappened: the answer is shown either way.
func TestInboxResumedSaysWhatHappened(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	m.applyInboxResumed(InboxResumedMsg{Command: "claude --resume 5f1c"})
	m.applyInboxResumed(InboxResumedMsg{Err: errors.New("the pane is running a program")})
	if len(m.Notifications) != 2 ||
		!strings.Contains(m.Notifications[0].Message, "Resumed: claude --resume 5f1c") ||
		!strings.Contains(m.Notifications[1].Message, "running a program") {
		t.Errorf("the answers were shown as %+v", m.Notifications)
	}
}
