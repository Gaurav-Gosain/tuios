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

// TestInboxOpensOnAQuestionFromThePaneInFront is the popup: a question from
// the pane this client shows opens the Inbox on it, and a question from any
// other pane leaves the keyboard alone and raises an alert instead.
func TestInboxOpensOnAQuestionFromThePaneInFront(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	m.FocusedWindow = 0
	m.applyInboxEvents(opened(askItem("1", "here", "w-1", "yes", "no")))
	if !m.ShowInbox || m.Inbox.Filter != session.AttentionAsk {
		t.Fatalf("a question from the focused pane did not open the Inbox (open=%v filter=%q)", m.ShowInbox, m.Inbox.Filter)
	}
	if it, _ := m.inboxSelected(); it.ID != "1" {
		t.Fatalf("the Inbox opened on %q, want the question", it.ID)
	}
	if len(m.Notifications) != 0 {
		t.Fatalf("a question put in front of the person also raised an alert: %+v", m.Notifications)
	}

	m.CloseInbox()
	m.applyInboxEvents(opened(askItem("2", "here", "w-2", "yes")))
	if m.ShowInbox {
		t.Fatal("a question from a pane the person is not looking at took the keyboard")
	}
	if n := lastNote(m); !strings.Contains(n, "has a question") {
		t.Fatalf("a question from another pane raised no alert: %q", n)
	}
}

// TestNextAttentionOpensTheInboxOnAQuestion: prefix o visits what needs the
// person, and a question is answered in the Inbox rather than in its pane, so
// it opens the Inbox on it.
func TestNextAttentionOpensTheInboxOnAQuestion(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{askItem("1", "work", "", "yes", "no")}})
	m.JumpToNextAttention()
	if !m.ShowInbox {
		t.Fatal("the next item that needs you is a question, and the Inbox did not open on it")
	}
	if it, _ := m.inboxSelected(); it.ID != "1" {
		t.Fatalf("the Inbox opened on %q, want the question", it.ID)
	}
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

// TestAQuestionDoesNotPopUnderSomeonesHands keeps the popup from taking keys
// meant for something else. A question from the pane in front of the person
// only alerts, and leaves the keyboard alone, when they typed into the pane a
// moment ago or when an overlay is open. An Inbox that is already open keeps
// its cursor and its peek.
func TestAQuestionDoesNotPopUnderSomeonesHands(t *testing.T) {
	t.Run("typing into the pane", func(t *testing.T) {
		m := inboxOS(t, zeroSettle())
		m.FocusedWindow = 0
		m.NotePaneKey()
		m.applyInboxEvents(opened(askItem("1", "here", "w-1", "yes", "no")))
		if m.ShowInbox {
			t.Fatal("a question popped while the person was typing into its pane")
		}
		if n := lastNote(m); !strings.Contains(n, "has a question") {
			t.Fatalf("a question that did not pop raised no alert: %q", n)
		}
		m.Inbox.paneKeyAt = time.Now().Add(-time.Minute)
		m.applyInboxEvents(opened(askItem("2", "here", "w-1", "yes", "no")))
		if !m.ShowInbox {
			t.Fatal("a question did not pop once the person had stopped typing")
		}
	})
	t.Run("the Inbox already open", func(t *testing.T) {
		m := inboxOS(t, zeroSettle())
		m.FocusedWindow = 0
		m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{item("7", session.AttentionErrored, "far", "w-9", "", 1)}})
		m.OpenInbox("")
		m.Inbox.SelectedID = "7"
		m.applyInboxEvents(opened(askItem("1", "here", "w-1", "yes", "no")))
		if m.Inbox.SelectedID != "7" || !m.Inbox.poppedAt.IsZero() {
			t.Fatalf("a question moved the cursor of an open Inbox to %q", m.Inbox.SelectedID)
		}
		if n := lastNote(m); !strings.Contains(n, "has a question") {
			t.Fatalf("a question that did not pop raised no alert: %q", n)
		}
	})
	t.Run("another overlay open", func(t *testing.T) {
		m := inboxOS(t, zeroSettle())
		m.FocusedWindow = 0
		m.ShowAgentMail = true
		m.applyInboxEvents(opened(askItem("1", "here", "w-1", "yes", "no")))
		if m.ShowInbox {
			t.Fatal("a question popped over the mailbox")
		}
	})
}
