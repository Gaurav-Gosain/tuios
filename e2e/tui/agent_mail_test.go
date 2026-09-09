package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// saveFrame writes the screen to $TUIOS_E2E_FRAMES/<name>.txt when that
// directory is set, so a run can hand a real frame to whoever asked for proof.
func saveFrame(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	dir := os.Getenv("TUIOS_E2E_FRAMES")
	if dir == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(term.Snapshot()), 0o644); err != nil {
		t.Logf("could not save frame %s: %v", name, err)
	}
}

// TestMailboxEmptyStateTeaches opens the mailbox on a session with no mail. The
// overlay has to say what it is and what makes something appear here, because
// that is the state most people meet it in.
func TestMailboxEmptyStateTeaches(t *testing.T) {
	term := attachClient(t)

	if err := term.SendKeys(tuitest.Ctrl('b'), "M"); err != nil {
		t.Fatalf("open the mailbox: %v", err)
	}
	for _, want := range []string{"Mail", "No mail.", "send-agent-message"} {
		if err := term.WaitForText(want, uiTimeout); err != nil {
			t.Fatalf("the empty mailbox never said %q: %v\n%s", want, err, term.Snapshot())
		}
	}
	saveFrame(t, term, "mail-empty")

	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close the mailbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "No mail.")
	}, uiTimeout); err != nil {
		t.Fatalf("esc did not close the mailbox: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after closing the empty mailbox")
}

// TestAgentMailReachesThePersonAndTheReplyReachesTheRing drives the whole loop
// against a real daemon and a real client: an agent writes to human over the
// CLI, exactly as an agent would; the dock announces it; the mailbox opens on
// the leader chord, lists the thread, reads it, and takes a reply; and the
// reply is in the ring, from human, threaded on the question, where the agent
// reads it back with the same CLI.
//
// Negative control: with the push in verbSendAgentMessage cut, the dock never
// announces and the first wait fails; with the leader binding cut, the second.
func TestAgentMailReachesThePersonAndTheReplyReachesTheRing(t *testing.T) {
	term, base := attachClientBase(t)
	renameWindow(t, term, "REVIEWER")

	if out, err := tuiosCLI(t, base, "send-agent-message", "-s", "e2e-ctrlp", "-w", "human",
		"--from", "REVIEWER", "--subject", "which retry policy?", "exponential or fixed? both pass"); err != nil {
		t.Fatalf("send-agent-message failed: %v\n%s", err, out)
	}

	// The person hears about it without going looking.
	if err := term.WaitForText("REVIEWER to you: which retry policy?", uiTimeout); err != nil {
		t.Fatalf("the dock never announced mail to the person: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "mail-dock")

	// The leader chord opens the list, which names the thread.
	if err := term.SendKeys(tuitest.Ctrl('b'), "M"); err != nil {
		t.Fatalf("open the mailbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "REVIEWER") && strings.Contains(text, "which retry policy?") && strings.Contains(text, "unread")
	}, uiTimeout); err != nil {
		t.Fatalf("the mailbox never listed the thread: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "mail-list")

	// Enter reads it.
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("open the thread: %v", err)
	}
	if err := term.WaitForText("exponential or fixed? both pass", uiTimeout); err != nil {
		t.Fatalf("the thread view never showed the body: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "mail-thread")

	// r, the answer, enter.
	if err := term.SendKeys("r"); err != nil {
		t.Fatalf("open the reply line: %v", err)
	}
	if err := term.WaitForText("reply:", uiTimeout); err != nil {
		t.Fatalf("r did not open the reply line: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("take exponential", tuitest.Enter); err != nil {
		t.Fatalf("type and send the reply: %v", err)
	}
	// The daemon stores it and pushes it back; the thread view follows.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "take exponential") && strings.Contains(text, "you") && !strings.Contains(text, "reply:")
	}, uiTimeout); err != nil {
		t.Fatalf("the reply never came back into the thread: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "mail-replied")

	// And the agent reads the answer with the CLI, threaded on its question.
	out, err := tuiosCLI(t, base, "read-agent-messages", "-s", "e2e-ctrlp", "--peek", "--json")
	if err != nil {
		t.Fatalf("read-agent-messages failed: %v\n%s", err, out)
	}
	var ring struct {
		Messages []struct {
			ID        uint64 `json:"id"`
			From      string `json:"from"`
			FromLabel string `json:"from_label"`
			To        string `json:"to"`
			Text      string `json:"text"`
			ReplyTo   uint64 `json:"reply_to"`
			ThreadID  uint64 `json:"thread_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out), &ring); err != nil {
		t.Fatalf("read-agent-messages returned no JSON: %v\n%s", err, out)
	}
	if len(ring.Messages) != 2 {
		t.Fatalf("the ring holds %d message(s), want the question and the reply:\n%s", len(ring.Messages), out)
	}
	question, reply := ring.Messages[0], ring.Messages[1]
	if question.To != "human" {
		t.Errorf("the question is addressed to %q, want human", question.To)
	}
	if reply.From != "human" || reply.FromLabel != "human" {
		t.Errorf("the reply is from %q (%q), want human", reply.From, reply.FromLabel)
	}
	if reply.Text != "take exponential" {
		t.Errorf("the reply reads %q, want what was typed", reply.Text)
	}
	if reply.ReplyTo != question.ID || reply.ThreadID != question.ThreadID {
		t.Errorf("the reply answers %d in thread %d, want %d in thread %d", reply.ReplyTo, reply.ThreadID, question.ID, question.ThreadID)
	}
	if reply.To != question.From {
		t.Errorf("the reply is addressed to %q, want the agent that asked, %q", reply.To, question.From)
	}

	// Once read, the person's inbox is empty for the daemon too.
	out, err = tuiosCLI(t, base, "list-agents", "-s", "e2e-ctrlp", "--json")
	if err != nil {
		t.Fatalf("list-agents failed: %v\n%s", err, out)
	}
	var listed struct {
		HumanUnread int `json:"human_unread"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("list-agents returned no JSON: %v\n%s", err, out)
	}
	if listed.HumanUnread != 0 {
		t.Errorf("after reading the thread the daemon still counts %d unread for the person", listed.HumanUnread)
	}
	alive(t, term, "after replying from the mailbox")
}
