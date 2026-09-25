package app

import (
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

// TestInboxReplyApprovalOnlySendsWhatThePromptTakes: a key that does not answer the
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

// lastNote is the newest notification's message, or empty.
func lastNote(m *OS) string {
	if len(m.Notifications) == 0 {
		return ""
	}
	return m.Notifications[len(m.Notifications)-1].Message
}

// settleShown makes the held approval last drawn count as on screen long
// enough to have been read.
func settleShown(m *OS) {
	m.Inbox.shown.since = time.Now().Add(-time.Minute)
}

// reachedSend says an answer passed every check the Inbox makes and got as
// far as sending, which a test OS with no daemon client refuses.
func reachedSend(m *OS) bool {
	return strings.Contains(lastNote(m), "needs a client attached")
}

// TestInboxSelectionFollowsTheItem: an item that arrives and sorts above the
// cursor does not move a different prompt under it. The cursor stays on the
// item the person selected.
func TestInboxSelectionFollowsTheItem(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	now := time.Now().UnixNano()
	a := heldApproval("1", "r1", session.ApprovalOnce, session.ApprovalDeny)
	a.Since = now - 2e9
	b := heldApproval("2", "r2", session.ApprovalOnce, session.ApprovalDeny)
	b.Since = now - 1e9
	b.Summary = "approve Bash: ls"
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{a, b}})
	m.OpenInbox("")
	m.InboxMove(1)
	if it, _ := m.inboxSelected(); it.ID != "2" {
		t.Fatalf("selected %s, want 2", it.ID)
	}
	m.renderInbox()

	// An older approval opens and sorts first, pushing both down a row.
	c := heldApproval("3", "r3", session.ApprovalOnce, session.ApprovalDeny)
	c.Since = now - 3e9
	c.Summary = "approve Bash: rm -rf build"
	m.applyInboxEvents(opened(c))
	if it, _ := m.inboxSelected(); it.ID != "2" {
		t.Fatalf("after a re-sort the cursor is on %s (%q), want the selected item 2", it.ID, it.Summary)
	}
	settleShown(m)
	m.InboxReplyApproval(session.ApprovalOnce)
	if !reachedSend(m) {
		t.Fatalf("the selected, settled prompt was not answered: %q", lastNote(m))
	}

	// When the selected item closes, the cursor lands on a neighbour, which
	// is a prompt the person has not read: it does not answer until drawn.
	closed := b
	closed.Closed = session.AttentionClosedResolved
	m.applyInboxEvents(InboxEventsMsg{Events: []InboxEvent{{Action: session.AttentionClosed, Item: &closed}}})
	if it, _ := m.inboxSelected(); it.ID == "2" {
		t.Fatal("the cursor is on a closed item")
	}
	m.InboxReplyApproval(session.ApprovalOnce)
	if reachedSend(m) {
		t.Fatal("a prompt that moved under the cursor was answered before it was drawn")
	}
}

// TestInboxAnswersOnlyWhatWasOnScreen: a key answers the held approval only as
// it was drawn, and only once it has been on screen long enough to read. A
// prompt that just appeared or just changed its line is not answered.
func TestInboxAnswersOnlyWhatWasOnScreen(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	held := heldApproval("1", "r1", session.ApprovalOnce, session.ApprovalDeny)
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{held}})
	m.OpenInbox("")

	// Never drawn.
	m.InboxReplyApproval(session.ApprovalOnce)
	if reachedSend(m) || !strings.Contains(lastNote(m), "just changed") {
		t.Fatalf("a prompt never drawn was answered: %q", lastNote(m))
	}
	// Drawn this instant.
	m.renderInbox()
	m.InboxReplyApproval(session.ApprovalOnce)
	if reachedSend(m) {
		t.Fatal("a prompt drawn this instant was answered")
	}
	// Drawn and read.
	settleShown(m)
	m.InboxReplyApproval(session.ApprovalOnce)
	if !reachedSend(m) {
		t.Fatalf("a settled prompt was not answered: %q", lastNote(m))
	}

	// The same hold on a new line, between the render and the key.
	changed := held
	changed.Summary = "approve Bash: rm -rf ~"
	m.applyInboxEvents(InboxEventsMsg{Events: []InboxEvent{{Action: session.AttentionUpdated, Item: &changed}}})
	m.InboxReplyApproval(session.ApprovalOnce)
	if reachedSend(m) {
		t.Fatal("a key answered a line that was never drawn")
	}
	m.renderInbox()
	settleShown(m)
	m.InboxReplyApproval(session.ApprovalOnce)
	if !reachedSend(m) {
		t.Fatalf("the new line, once read, was not answered: %q", lastNote(m))
	}

	// A new hold on the same line is a new request, drawn afresh.
	renewed := changed
	renewed.RequestID = "r2"
	m.applyInboxEvents(InboxEventsMsg{Events: []InboxEvent{{Action: session.AttentionUpdated, Item: &renewed}}})
	m.InboxReplyApproval(session.ApprovalOnce)
	if reachedSend(m) {
		t.Fatal("a new hold was answered before it was drawn")
	}
}

// TestInboxShowsTheWholePrompt: the row cuts the line, so the held approval
// under the cursor is shown whole below the list, with the rules always adds
// beside its key.
func TestInboxShowsTheWholePrompt(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	held := heldApproval("1", "r1", session.ApprovalOnce, session.ApprovalAlways, session.ApprovalDeny)
	tail := "&& echo the-end-of-the-command"
	held.Summary = "approve Bash: go test ./internal/session/ ./internal/app/ ./cmd/tuios/ -run Approval -count=1 " + tail
	held.AlwaysScope = []string{"Bash(go test:*) in .claude/settings.local.json"}
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{held}})
	m.OpenInbox("")
	out, _, _ := m.renderInbox()
	plain := ansi.Strip(out)
	flat := strings.Join(strings.Fields(plain), " ")
	for _, want := range []string{"the-end-of-the-command", "2 (always) also allows from now on:", "Bash(go test:*) in .claude/settings.local.json"} {
		if !strings.Contains(flat, want) {
			t.Errorf("the Inbox does not show %q:\n%s", want, plain)
		}
	}

	// A line this client would draw with characters left out is not
	// answered here.
	odd := held
	odd.Summary = "approve Bash: echo \u200bhi"
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{odd}})
	out, _, _ = m.renderInbox()
	if !strings.Contains(strings.Join(strings.Fields(ansi.Strip(out)), " "), "not answered here") {
		t.Errorf("a line with a hidden character reads as answerable:\n%s", ansi.Strip(out))
	}
	settleShown(m)
	m.InboxReplyApproval(session.ApprovalOnce)
	if reachedSend(m) || !strings.Contains(lastNote(m), "cannot be shown whole") {
		t.Fatalf("a line with a hidden character was answered: %q", lastNote(m))
	}
}
