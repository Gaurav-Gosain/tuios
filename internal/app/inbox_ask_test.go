package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// askItem is a question ask-human put to the person about window.
func askItem(id, sess, window string, options ...string) session.AttentionItem {
	it := item(id, session.AttentionAsk, sess, window, "Deploy to staging?", time.Now().UnixNano())
	it.RequestID = "q" + id
	it.Options = options
	return it
}

// TestInboxAnswersAQuestionWithItsDigits: the question is shown whole with
// its answers numbered, a digit picks one once it has been read, and a digit
// the question does not take says so.
func TestInboxAnswersAQuestionWithItsDigits(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{askItem("1", "work", "w-9", "yes", "no", "later")}})
	m.OpenInbox("")

	m.InboxNumber(2)
	if reachedSend(m) || !strings.Contains(lastNote(m), "just appeared") {
		t.Fatalf("a question never drawn was answered: %q", lastNote(m))
	}
	out, _, _ := m.renderInbox()
	plain := ansi.Strip(out)
	for _, want := range []string{"Questions", "[1-3] Deploy to staging?", "2  no", "3  later", "answer"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the question does not show %q:\n%s", want, plain)
		}
	}
	settleShown(m)
	m.InboxNumber(4)
	if reachedSend(m) || !strings.Contains(lastNote(m), "takes 1 to 3") {
		t.Fatalf("a digit the question does not take: %q", lastNote(m))
	}
	m.InboxNumber(2)
	if !reachedSend(m) {
		t.Fatalf("a settled question was not answered: %q", lastNote(m))
	}

	// Enter goes to the pane that asked and sends nothing.
	if cmd := m.InboxActivate(); cmd != nil {
		t.Fatal("enter on a question sent something")
	}
}
