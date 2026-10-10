package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// mailSeenRow is the part of a read-agent-messages message these tests read.
type mailSeenRow struct {
	ID        uint64 `json:"id"`
	ReadAt    int64  `json:"read_at"`
	SeenAt    int64  `json:"seen_at"`
	WasUnread bool   `json:"was_unread"`
	WasSeen   bool   `json:"was_seen"`
}

// mailSeenRing reads a session's ring as JSON. args are the flags after
// read-agent-messages -s e2e-ctrlp.
func mailSeenRing(t *testing.T, base string, args ...string) []mailSeenRow {
	t.Helper()
	argv := append([]string{"read-agent-messages", "-s", "e2e-ctrlp", "--json"}, args...)
	out, err := tuiosCLI(t, base, argv...)
	if err != nil {
		t.Fatalf("%v failed: %v\n%s", argv, err, out)
	}
	var res struct {
		Messages   []mailSeenRow `json:"messages"`
		Unread     int           `json:"unread"`
		Seen       int           `json:"seen"`
		MarkedRead int           `json:"marked_read"`
		MarkedSeen int           `json:"marked_seen"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v returned no JSON: %v\n%s", argv, err, out)
	}
	return res.Messages
}

// mailSeenLog collects the CLI output a run shows, and writes it to
// $TUIOS_E2E_FRAMES/<name>.txt at the end, so a run leaves the proof behind.
type mailSeenLog struct {
	t    *testing.T
	base string
	name string
	buf  strings.Builder
}

func (l *mailSeenLog) run(args ...string) string {
	l.t.Helper()
	out, err := tuiosCLI(l.t, l.base, args...)
	if err != nil {
		l.t.Fatalf("%v failed: %v\n%s", args, err, out)
	}
	fmt.Fprintf(&l.buf, "$ tuios %s\n%s\n", strings.Join(args, " "), out)
	return out
}

func (l *mailSeenLog) save() {
	dir := os.Getenv("TUIOS_E2E_FRAMES")
	if dir == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, l.name+".txt"), []byte(l.buf.String()), 0o644); err != nil {
		l.t.Logf("could not save %s: %v", l.name, err)
	}
}

func mailSeenWant(t *testing.T, out, want, when string) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Errorf("%s: the output has no %q:\n%s", when, want, out)
	}
}

// TestMailSeenStateBetweenUnreadAndRead follows one message through the three
// states. A peek of the recipient's inbox marks it seen and not read, the
// sender's view of the session says seen, list-agents counts it as unread and
// seen, and a marking read ends with a summary that states the state after the
// read ("marked read"), not the count from before it.
//
// Negative control: with markSeen left false in verbReadAgentMessages, the peek
// stamps nothing and the first seen check fails; with the summary counting res.Unread
// again, the marking read prints "1 unread".
func TestMailSeenStateBetweenUnreadAndRead(t *testing.T) {
	_, base := attachClientBase(t)
	log := &mailSeenLog{t: t, base: base, name: "mail-seen-states"}
	defer log.save()

	// The first window is the recipient and a second one is the sender.
	log.run("set-window", "-s", "e2e-ctrlp", "--name", "WORKER")
	log.run("new-window", "-s", "e2e-ctrlp", "SENDER", "--no-focus")
	log.run("send-agent-message", "-s", "e2e-ctrlp", "-w", "WORKER", "--from", "SENDER", "--subject", "retest", "please retest")

	// Sent: unread, and a read of the session shows no seen mark.
	out := log.run("read-agent-messages", "-s", "e2e-ctrlp")
	mailSeenWant(t, out, "1 message, 1 unread.", "the sender's view of a new message")
	if strings.Contains(out, "  seen") {
		t.Errorf("a message nobody looked at is marked seen:\n%s", out)
	}
	if rows := mailSeenRing(t, base); len(rows) != 1 || rows[0].SeenAt != 0 || rows[0].ReadAt != 0 {
		t.Fatalf("a new message has seen_at or read_at set: %+v", rows)
	}

	// A peek with no inbox named marks nothing: nobody looked as the recipient.
	log.run("read-agent-messages", "-s", "e2e-ctrlp", "--peek")
	if rows := mailSeenRing(t, base); rows[0].SeenAt != 0 {
		t.Fatalf("a peek of the whole session marked the message seen: %+v", rows)
	}

	// The recipient peeks: seen, not read.
	out = log.run("read-agent-messages", "-s", "e2e-ctrlp", "-w", "WORKER", "--peek")
	mailSeenWant(t, out, "1 message, marked seen.", "the recipient's peek")
	rows := mailSeenRing(t, base)
	if rows[0].SeenAt == 0 || rows[0].ReadAt != 0 {
		t.Fatalf("after a peek the message should be seen and unread: %+v", rows)
	}
	firstSeen := rows[0].SeenAt

	// The sender's view.
	out = log.run("read-agent-messages", "-s", "e2e-ctrlp")
	mailSeenWant(t, out, "  seen", "the sender's view after a peek")
	mailSeenWant(t, out, "1 message, 1 seen.", "the sender's view after a peek")
	out = log.run("list-agents", "-s", "e2e-ctrlp", "--all")
	mailSeenWant(t, out, "1 (1 seen)", "list-agents after a peek")
	if rows := mailSeenRing(t, base, "--unread"); len(rows) != 1 {
		t.Errorf("--unread should still return a seen message, got %d", len(rows))
	}

	// A second peek keeps the first look.
	log.run("read-agent-messages", "-s", "e2e-ctrlp", "-w", "WORKER", "--peek")
	if rows := mailSeenRing(t, base); rows[0].SeenAt != firstSeen {
		t.Errorf("a second peek moved seen_at from %d to %d", firstSeen, rows[0].SeenAt)
	}

	// The marking read: the summary states the state after the call.
	out = log.run("read-agent-messages", "-s", "e2e-ctrlp", "-w", "WORKER")
	mailSeenWant(t, out, "1 message, marked read.", "the marking read")
	if strings.Contains(out, "1 unread") {
		t.Errorf("the marking read still counts the message unread:\n%s", out)
	}
	rows = mailSeenRing(t, base)
	if rows[0].ReadAt == 0 || rows[0].SeenAt != firstSeen {
		t.Fatalf("after a read the message should be read and keep seen_at: %+v", rows)
	}

	// The sender's view of a read message.
	out = log.run("read-agent-messages", "-s", "e2e-ctrlp")
	mailSeenWant(t, out, "1 message, all read.", "the sender's view after a read")
	if strings.Contains(out, "  seen") || strings.Contains(out, "  new") {
		t.Errorf("a read message is still marked:\n%s", out)
	}
	out = log.run("list-agents", "-s", "e2e-ctrlp", "--all")
	if strings.Contains(out, "seen)") {
		t.Errorf("list-agents counts a read message as seen:\n%s", out)
	}
}

// mailSeenToPerson sends a message to the person from a pane named REVIEWER
// and waits for the dock to announce it.
func mailSeenToPerson(t *testing.T, term *tuitest.Terminal, log *mailSeenLog) {
	t.Helper()
	renameWindow(t, term, "REVIEWER")
	log.run("send-agent-message", "-s", "e2e-ctrlp", "-w", "human", "--from", "REVIEWER", "--subject", "which retry policy?", "exponential or fixed?")
	if err := term.WaitForText("REVIEWER to you: which retry policy?", uiTimeout); err != nil {
		t.Fatalf("the dock never announced mail to the person: %v\n%s", err, term.Snapshot())
	}
}

// mailSeenPersonPeek peeks the person's mail from the CLI, which runs outside
// any pane here and so may act as the person.
func mailSeenPersonPeek(t *testing.T, log *mailSeenLog) {
	t.Helper()
	mailSeenWant(t, log.run("read-agent-messages", "-s", "e2e-ctrlp", "-w", "human", "--peek"), "marked seen.", "the person's peek")
	mailSeenWant(t, log.run("list-attention", "-s", "e2e-ctrlp"), "[1 seen]", "list-attention after a peek")
}

// TestMailSeenInboxRow peeks the person's mail while the Inbox is open and
// checks that the row says seen without the client doing anything.
//
// Negative control: with noteMailSeen cut from verbReadAgentMessages, the row
// never changes and the wait fails.
func TestMailSeenInboxRow(t *testing.T) {
	term, base := attachClientBase(t)
	log := &mailSeenLog{t: t, base: base, name: "mail-seen-inbox"}
	defer log.save()
	mailSeenToPerson(t, term, log)

	if err := term.SendKeys(tuitest.Ctrl('b'), "M"); err != nil {
		t.Fatalf("open the Inbox on mail: %v", err)
	}
	if err := term.WaitForText("@ REVIEWER  which retry policy?", uiTimeout); err != nil {
		t.Fatalf("the Inbox never listed the thread: %v\n%s", err, term.Snapshot())
	}
	mailSeenPersonPeek(t, log)
	if err := term.WaitForText("@ REVIEWER seen  which retry policy?", uiTimeout); err != nil {
		t.Fatalf("the Inbox row never said seen: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "mail-seen-inbox")
	alive(t, term, "after the seen mail")
}

// TestMailSeenMailbox peeks the person's mail while the mailbox is open and
// checks that its list and the thread view say seen, and that opening the
// thread, which is the person reading it, ends the seen state.
//
// Negative control: with the SeenIDs branch cut from noteAgentMail, the list
// keeps saying unread and the wait fails; with the "seen" case cut from
// agentMailThreadRow, the list says unread too.
func TestMailSeenMailbox(t *testing.T) {
	term, base := attachClientBase(t)
	log := &mailSeenLog{t: t, base: base, name: "mail-seen-mailbox"}
	defer log.save()
	mailSeenToPerson(t, term, log)

	if err := term.SendKeys(tuitest.Ctrl('b'), "M", "m"); err != nil {
		t.Fatalf("open the mailbox: %v", err)
	}
	if err := term.WaitForText("unread  1 ", uiTimeout); err != nil {
		t.Fatalf("the mailbox list never said unread: %v\n%s", err, term.Snapshot())
	}
	mailSeenPersonPeek(t, log)
	if err := term.WaitForText("seen  1 ", uiTimeout); err != nil {
		t.Fatalf("the mailbox list never changed to seen: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "mail-seen-list")

	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("open the thread: %v", err)
	}
	if err := term.WaitForText("exponential or fixed?", uiTimeout); err != nil {
		t.Fatalf("the thread never opened: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "mail-seen-thread")
	// Opening the thread is the person reading it: nothing is left to list.
	if err := term.WaitFor(func(tuitest.Screen) bool {
		out, err := tuiosCLI(t, base, "list-attention", "-s", "e2e-ctrlp")
		return err == nil && !strings.Contains(out, "retry policy")
	}, uiTimeout); err != nil {
		t.Fatalf("the mail item stayed in the Inbox after the person read it: %v", err)
	}
	alive(t, term, "after the seen mail")
}
