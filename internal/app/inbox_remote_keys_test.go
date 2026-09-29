package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestInboxDigitsFromSendKeysAnswerNothing: send-keys with no window hands
// its keys to the attached client, which reads them as the person's. The
// Inbox's digit keys answer a held approval or a question as the person, so a
// digit that send-keys typed must answer nothing. The reason line, the peek
// and the reply queue already refuse such keys; the digits must too.
//
// Negative control: without the ProcessingRemoteKeys check in
// InboxReplyApproval, the first InboxNumber returns the reply command.
func TestInboxDigitsFromSendKeysAnswerNothing(t *testing.T) {
	m, r := approvalsOS(t, heldApproval("1", "r1", session.ApprovalOnce, session.ApprovalAlways, session.ApprovalDeny))
	m.ProcessingRemoteKeys = true
	press(m, "1")
	if cmd := m.InboxNumber(1); cmd != nil {
		cmd()
		t.Fatalf("a 1 from send-keys answered a held approval: %v", r.replies())
	}
	if !strings.Contains(lastNote(m), "keyboard") {
		t.Errorf("the refusal does not say only the keyboard answers: %q", lastNote(m))
	}
	m.ProcessingRemoteKeys = false
	press(m, "1")
	if cmd := m.InboxNumber(1); cmd == nil {
		t.Fatalf("a 1 from the keyboard did not answer: %q", lastNote(m))
	}
}

// TestInboxRiskySecondPressFromSendKeysAnswersNothing: the first press of an
// allow on a risky approval arms it, and the second sends it. A second press
// that send-keys typed must not complete what the person started.
func TestInboxRiskySecondPressFromSendKeysAnswersNothing(t *testing.T) {
	m, r := approvalsOS(t, riskyHeld())
	press(m, "1")
	if cmd := m.InboxNumber(1); cmd != nil {
		t.Fatal("the first press on a risky approval sent it")
	}
	m.ProcessingRemoteKeys = true
	press(m, "1")
	if cmd := m.InboxNumber(1); cmd != nil {
		cmd()
		t.Fatalf("a second press from send-keys allowed a risky call: %v", r.replies())
	}
	// Nor may send-keys arm it for the person's next press.
	press(m, "1")
	m.InboxNumber(1)
	m.ProcessingRemoteKeys = false
	press(m, "1")
	if cmd := m.InboxNumber(1); cmd != nil {
		t.Fatal("a press from send-keys armed a risky allow for the person's next press")
	}
}

// TestInboxQuestionDigitFromSendKeysAnswersNothing: a digit on a question
// ask-human put answers it as the person, and is held the same way.
func TestInboxQuestionDigitFromSendKeysAnswersNothing(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{askItem("1", "work", "w-9", "yes", "no")}})
	m.OpenInbox("")
	m.renderInbox()
	settleShown(m)
	m.ProcessingRemoteKeys = true
	m.InboxNumber(1)
	if reachedSend(m) {
		t.Fatal("a digit from send-keys answered a question")
	}
	m.ProcessingRemoteKeys = false
	m.InboxNumber(1)
	if !reachedSend(m) {
		t.Fatalf("a digit from the keyboard did not answer: %q", lastNote(m))
	}
}

// TestInboxReleaseFromSendKeysPassesNothing: passing on mail a link policy
// held for the person is their decision too.
func TestInboxReleaseFromSendKeysPassesNothing(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	it := item("1", session.AttentionMail, "work", "w-9", "mail from laptop", 1)
	it.HeldID = 7
	it.HeldFor = "w-9"
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{it}})
	m.OpenInbox("")
	m.ProcessingRemoteKeys = true
	if cmd := m.InboxRelease(); cmd != nil {
		t.Fatal("send-keys passed on held mail")
	}
	if !strings.Contains(lastNote(m), "keyboard") {
		t.Errorf("the refusal does not say only the keyboard passes mail on: %q", lastNote(m))
	}
}

// TestThePersonsKeyAnswersDuringASendKeysRun: ProcessingRemoteKeys stays set
// across the messages of one send-keys run. A key the person presses in
// between is theirs, and answers.
//
// Negative control: without the isPersonInput branch in Update, the person's
// 1 is refused as typed by send-keys.
func TestThePersonsKeyAnswersDuringASendKeysRun(t *testing.T) {
	m, _ := approvalsOS(t, heldApproval("1", "r1", session.ApprovalOnce, session.ApprovalAlways, session.ApprovalDeny))
	var answered tea.Cmd
	prev := getInputHandler()
	SetInputHandler(func(msg tea.Msg, o *OS) (tea.Model, tea.Cmd) {
		o.InboxKeyPressed("1")
		answered = o.InboxNumber(1)
		return o, nil
	})
	t.Cleanup(func() { SetInputHandler(prev) })
	m.ProcessingRemoteKeys = true
	m.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
	if answered == nil {
		t.Fatalf("the person's 1 was refused during a send-keys run: %q", lastNote(m))
	}
	if !m.ProcessingRemoteKeys {
		t.Error("the send-keys run lost its mark")
	}
	m.Update(RemoteKeyMsg{Key: tea.KeyPressMsg{Code: '1', Text: "1"}})
	if answered != nil {
		t.Error("a 1 from send-keys answered")
	}
}
