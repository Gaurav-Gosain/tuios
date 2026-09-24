package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// These tests pin the reply editor: r on a finished or errored item, r on a
// rail agent row, the refusal on a pane waiting on a prompt, the queue-prompt
// it sends as the person, the refusal of a draft send-keys typed, x on a rail
// row, and u undoing that drop.

// queueFake answers the queue verbs and records what it was sent.
type queueFake struct {
	calls   []queueCall
	err     error
	entries []map[string]any
	nextID  int
}

type queueCall struct {
	verb   string
	params map[string]any
}

func (q *queueFake) call(verb string, params map[string]any, _ time.Duration) (json.RawMessage, error) {
	q.calls = append(q.calls, queueCall{verb, params})
	if q.err != nil {
		return nil, q.err
	}
	switch verb {
	case "queue-prompt":
		q.nextID++
		id := "q" + string(rune('0'+q.nextID))
		q.entries = append(q.entries, map[string]any{"id": id, "state": "waiting"})
		return json.Marshal(map[string]any{"type": "prompt_queued", "id": id, "position": len(q.entries), "queued": len(q.entries), "delivering": false})
	case "list-queued":
		return json.Marshal(map[string]any{"type": "queued_prompts", "entries": q.entries})
	case "cancel-queued":
		id, _ := params["id"].(string)
		for i, e := range q.entries {
			if e["id"] == id {
				q.entries = append(q.entries[:i], q.entries[i+1:]...)
				break
			}
		}
		return json.Marshal(map[string]any{"type": "queue_cancelled", "cancelled": []string{id}, "queued": len(q.entries)})
	case "agent-activity":
		return json.Marshal(map[string]any{"type": "agent_activity", "entries": []any{}, "recap": map[string]any{"turns": 0, "files": []string{}, "state": "idle"}})
	}
	return nil, &session.VerbCallError{Code: session.ErrVerbUnknownVerb, Message: verb}
}

// last is the last call of verb.
func (q *queueFake) last(verb string) (map[string]any, bool) {
	for i := len(q.calls) - 1; i >= 0; i-- {
		if q.calls[i].verb == verb {
			return q.calls[i].params, true
		}
	}
	return nil, false
}

// replyOS is a client with an Inbox and a fake queue behind it.
func replyOS(t *testing.T, items ...session.AttentionItem) (*OS, *queueFake) {
	t.Helper()
	forgetSidebarState(t)
	m := inboxOS(t, zeroSettle())
	q := &queueFake{}
	m.SetInboxVerbCaller(q.call, func() string { return "nonce-1" })
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: items})
	return m, q
}

// runMsg runs a command and hands its message to Update.
func runMsg(t *testing.T, m *OS, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run")
	}
	m.Update(cmd())
}

// TestInboxReplyQueuesAsThePerson: r on a finished item opens the editor, what
// is typed is sent with queue-prompt and the attach nonce to that pane, and
// the dock says it is queued.
func TestInboxReplyQueuesAsThePerson(t *testing.T) {
	done := item("1", session.AttentionFinished, "here", "w-2", "Added retry", time.Now().Add(-4*time.Minute).UnixNano())
	done.Name = "api"
	m, q := replyOS(t, done)
	m.Windows[1].AgentState = "idle"
	m.OpenInbox("")
	if cmd := m.InboxReply(); cmd != nil {
		t.Fatal("opening the editor ran a command")
	}
	if !m.InboxReplyOpen() {
		t.Fatal("r on a finished item did not open the reply editor")
	}
	m.InboxReplyType("make the jitter")
	m.InboxReplyType(" configurable")
	out, _, _ := m.renderInbox()
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "Reply to api (done 4m): make the jitter configurable_") {
		t.Errorf("the editor does not read as the plan says:\n%s", plain)
	}
	if !strings.Contains(plain, "send when ready") || !strings.Contains(plain, "cancel") {
		t.Errorf("the footer is not the editor's:\n%s", plain)
	}
	runMsg(t, m, m.InboxReplySend())
	p, ok := q.last("queue-prompt")
	if !ok {
		t.Fatal("no queue-prompt was sent")
	}
	if p["session"] != "here" || p["window"] != "w-2" || p["text"] != "make the jitter configurable" || p["human_nonce"] != "nonce-1" {
		t.Errorf("queue-prompt got %v", p)
	}
	if m.InboxReplyOpen() {
		t.Error("the editor stayed open after sending")
	}
	if n := lastNotice(m); !strings.Contains(n.Message, "Queued for api") {
		t.Errorf("the dock said %q", n.Message)
	}
}

// TestInboxReplyIsOnlyForFinishedAndErrored: r on mail still opens the thread
// path, and on any other kind says what it does, with no editor.
func TestInboxReplyIsOnlyForFinishedAndErrored(t *testing.T) {
	m, _ := replyOS(t, item("1", session.AttentionQuestion, "here", "w-2", "which?", 1))
	m.OpenInbox("")
	m.InboxReply()
	if m.InboxReplyOpen() {
		t.Error("r on a question opened the reply editor")
	}
	errored := item("2", session.AttentionErrored, "here", "w-1", "crashed", 1)
	m, _ = replyOS(t, errored)
	m.OpenInbox("")
	m.InboxReply()
	if !m.InboxReplyOpen() {
		t.Error("r on an errored item did not open the editor")
	}
}

// TestReplyRefusedOnAPromptPane: a pane waiting on a prompt is answered, not
// replied to.
func TestReplyRefusedOnAPromptPane(t *testing.T) {
	m, q := replyOS(t)
	m.Windows[1].AgentState = "needs_input"
	m.Windows[1].CustomName = "api"
	cmd, handled := m.SidebarAgentReply("here", "w-2")
	if !handled || cmd != nil || m.InboxReplyOpen() || m.ShowInbox {
		t.Fatalf("r on a prompt pane: handled %v, editor %v, inbox %v", handled, m.InboxReplyOpen(), m.ShowInbox)
	}
	if n := lastNotice(m); n.Message != "api is waiting on a prompt. Answer it first (space to peek)." {
		t.Errorf("the dock said %q", n.Message)
	}
	if len(q.calls) != 0 {
		t.Errorf("a refused reply called %v", q.calls)
	}
}

// TestRailReplyOpensAndClosesTheInbox: r on a rail agent row opens the Inbox
// with the editor, even with nothing listed, and closes it on send or cancel.
func TestRailReplyOpensAndClosesTheInbox(t *testing.T) {
	m, q := replyOS(t)
	m.Windows[1].AgentState = "working"
	m.Windows[1].CustomName = "api"
	if _, handled := m.SidebarAgentReply("here", "w-2"); !handled || !m.InboxReplyOpen() {
		t.Fatal("r on a working agent row did not open the editor")
	}
	out, _, _ := m.renderInbox()
	if plain := ansi.Strip(out); !strings.Contains(plain, "Reply to api (working): _") {
		t.Errorf("the empty Inbox does not show the editor:\n%s", plain)
	}
	m.InboxReplyCancel()
	if m.ShowInbox {
		t.Error("cancel left the Inbox the rail opened")
	}
	m.SidebarAgentReply("here", "w-2")
	m.InboxReplyType("go on")
	runMsg(t, m, m.InboxReplySend())
	if m.ShowInbox {
		t.Error("send left the Inbox the rail opened")
	}
	if p, _ := q.last("queue-prompt"); p["window"] != "w-2" {
		t.Errorf("queue-prompt got %v", p)
	}
}

// TestReplyTypedBySendKeysIsNotSent: a draft that any key from send-keys
// touched is refused, since the person's queued message is typed without a
// check of whoever drove the keys.
func TestReplyTypedBySendKeysIsNotSent(t *testing.T) {
	done := item("1", session.AttentionFinished, "here", "w-2", "done", 1)
	m, q := replyOS(t, done)
	m.OpenInbox("")
	m.InboxReply()
	m.ProcessingRemoteKeys = true
	m.InboxReplyType("rm -rf /")
	m.ProcessingRemoteKeys = false
	if cmd := m.InboxReplySend(); cmd != nil {
		t.Fatal("a draft send-keys typed was sent")
	}
	if len(q.calls) != 0 {
		t.Errorf("calls were made: %v", q.calls)
	}
	if n := lastNotice(m); !strings.Contains(n.Message, "send-keys") {
		t.Errorf("the dock said %q", n.Message)
	}
}

// TestReplyOnAnOlderDaemon: a daemon without queue-prompt answers
// unknown_verb, and r then does what it did before it was bound.
func TestReplyOnAnOlderDaemon(t *testing.T) {
	done := item("1", session.AttentionFinished, "here", "w-2", "done", 1)
	m, q := replyOS(t, done)
	q.err = &session.VerbCallError{Code: session.ErrVerbUnknownVerb, Message: "queue-prompt"}
	m.OpenInbox("")
	m.InboxReply()
	m.InboxReplyType("more")
	runMsg(t, m, m.InboxReplySend())
	if !strings.Contains(lastNotice(m).Message, "tuios kill-server") {
		t.Errorf("the dock said %q", lastNotice(m).Message)
	}
	m.InboxReply()
	if m.InboxReplyOpen() {
		t.Error("r opened the editor on a daemon that has no queue")
	}
	if _, handled := m.SidebarAgentReply("here", "w-2"); handled {
		t.Error("the rail's r did not fall through to rename")
	}
	for _, h := range m.inboxRowHints(done, true) {
		if h.Label == "reply" {
			t.Error("the footer offers reply on a daemon with no queue")
		}
	}
}

// TestRailDropAndUndo: x on a row with a queue drops the newest waiting
// message, and u on the same row within the undo window queues it again. A
// row with nothing queued leaves x to the rail.
func TestRailDropAndUndo(t *testing.T) {
	m, q := replyOS(t)
	m.Windows[1].AgentState = "working"
	m.Windows[1].CustomName = "api"
	if _, handled := m.SidebarAgentCancelQueued("here", "w-2"); handled {
		t.Fatal("x with nothing queued did not fall through to the rail")
	}

	m.SidebarAgentReply("here", "w-2")
	m.InboxReplyType("first")
	runMsg(t, m, m.InboxReplySend())
	m.SidebarAgentReply("here", "w-2")
	m.InboxReplyType("second")
	runMsg(t, m, m.InboxReplySend())
	m.Windows[1].AgentQueued = 2

	cmd, handled := m.SidebarAgentCancelQueued("here", "w-2")
	if !handled {
		t.Fatal("x with a queue did nothing")
	}
	runMsg(t, m, cmd)
	p, _ := q.last("cancel-queued")
	if p["id"] != "q2" || p["human_nonce"] != "nonce-1" {
		t.Errorf("cancel-queued got %v, want the newest with the nonce", p)
	}
	if n := lastNotice(m); n.Message != "Dropped 1 queued message to api. u undoes." {
		t.Errorf("the dock said %q", n.Message)
	}

	// u on another row marks unread as always.
	if _, ok := m.sidebarAgentUndoDrop("here", "w-1"); ok {
		t.Error("u on another row undid the drop")
	}
	cmd, ok := m.sidebarAgentUndoDrop("here", "w-2")
	if !ok {
		t.Fatal("u did not undo the drop")
	}
	runMsg(t, m, cmd)
	if p, _ := q.last("queue-prompt"); p["text"] != "second" {
		t.Errorf("the undo queued %v", p)
	}
	if _, ok := m.sidebarAgentUndoDrop("here", "w-2"); ok {
		t.Error("a second u undid the same drop again")
	}

	// A drop past the window is not undone.
	m.Inbox.reply.dropped = &inboxReplyDrop{session: "here", window: "w-2", text: "x", at: time.Now().Add(-inboxUndoWindow)}
	if _, ok := m.sidebarAgentUndoDrop("here", "w-2"); ok {
		t.Error("a drop past the undo window was undone")
	}
}

// TestRailDropOfAMessageQueuedElsewhere: a message this client did not queue
// is dropped without an undo, since its text is not here to queue again.
func TestRailDropOfAMessageQueuedElsewhere(t *testing.T) {
	m, q := replyOS(t)
	m.Windows[1].AgentState = "working"
	m.Windows[1].CustomName = "api"
	m.Windows[1].AgentQueued = 1
	q.entries = []map[string]any{{"id": "q7", "state": "waiting"}}
	cmd, _ := m.SidebarAgentCancelQueued("here", "w-2")
	runMsg(t, m, cmd)
	if n := lastNotice(m); n.Message != "Dropped 1 queued message to api" {
		t.Errorf("the dock said %q", n.Message)
	}
	if _, ok := m.sidebarAgentUndoDrop("here", "w-2"); ok {
		t.Error("u offered to undo a drop it cannot put back")
	}
	// An entry being typed cannot be dropped.
	q.entries = []map[string]any{{"id": "q8", "state": "delivering"}}
	cmd, _ = m.SidebarAgentCancelQueued("here", "w-2")
	runMsg(t, m, cmd)
	if _, sent := q.last("cancel-queued"); sent && q.calls[len(q.calls)-1].verb == "cancel-queued" {
		t.Error("a message being typed was sent to cancel-queued")
	}
}

// TestFinishedFooterOffersReply: a finished item's footer has r reply.
func TestFinishedFooterOffersReply(t *testing.T) {
	done := item("1", session.AttentionFinished, "here", "w-2", "done", 1)
	m, _ := replyOS(t, done)
	found := false
	for _, h := range m.inboxRowHints(done, true) {
		if h.Label == "reply" && h.Key == m.inboxKeyOr(config.ActionInboxReply, "r") {
			found = true
		}
	}
	if !found {
		t.Error("a finished item's footer does not offer reply")
	}
}
