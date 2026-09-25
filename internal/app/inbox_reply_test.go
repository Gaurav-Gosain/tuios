package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// These tests pin the reply editor's boundaries: no reply to a pane waiting
// on a prompt or a question, no draft or queue key from send-keys acting as
// the person, and the fallback on a daemon without queue-prompt.

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

// TestReplyRefusedOnAPaneWaitingOnYou: a pane waiting on a prompt is
// answered, not replied to. A pane with an ask-human question open reads as
// waiting on you on the rail, so r refuses it the same way, whatever the agent
// state the pane reported.
func TestReplyRefusedOnAPaneWaitingOnYou(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state string
		items []session.AttentionItem
	}{
		{"prompt", "needs_input", nil},
		{"ask-human question", "working", []session.AttentionItem{askItem("1", "here", "w-2", "yes", "no")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, q := replyOS(t, tc.items...)
			m.Windows[1].AgentState = tc.state
			m.Windows[1].CustomName = "api"
			cmd, handled := m.SidebarAgentReply("here", "w-2")
			if !handled || cmd != nil || m.InboxReplyOpen() || m.ShowInbox {
				t.Fatalf("r: handled %v, editor %v, inbox %v", handled, m.InboxReplyOpen(), m.ShowInbox)
			}
			if n := lastNotice(m); n.Message != "api is waiting on a prompt. Answer it first (space to peek)." {
				t.Errorf("the dock said %q", n.Message)
			}
			if len(q.calls) != 0 {
				t.Errorf("a refused reply called %v", q.calls)
			}
		})
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

// TestQueueKeysFromSendKeysAreRefused: x and u act on the queue as the
// person, with the attach nonce, so a key from send-keys or a tape does
// neither, and the drop stays for the person's own u.
func TestQueueKeysFromSendKeysAreRefused(t *testing.T) {
	m, q := replyOS(t)
	m.Windows[1].AgentState = "working"
	m.Windows[1].CustomName = "api"
	m.Windows[1].AgentQueued = 1
	q.entries = []map[string]any{{"id": "q7", "state": "waiting"}}

	m.ProcessingRemoteKeys = true
	cmd, handled := m.SidebarAgentCancelQueued("here", "w-2")
	m.ProcessingRemoteKeys = false
	if !handled || cmd != nil || len(q.calls) != 0 {
		t.Fatalf("x from send-keys: handled %v, command %v, calls %v", handled, cmd != nil, q.calls)
	}
	if n := lastNotice(m); !strings.Contains(n.Message, "send-keys") {
		t.Errorf("the dock said %q", n.Message)
	}

	m.Inbox.reply.dropped = &inboxReplyDrop{session: "here", window: "w-2", who: "api", text: "put back", at: time.Now()}
	m.ProcessingRemoteKeys = true
	cmd, handled = m.sidebarAgentUndoDrop("here", "w-2")
	m.ProcessingRemoteKeys = false
	if !handled || cmd != nil || len(q.calls) != 0 {
		t.Fatalf("u from send-keys: handled %v, command %v, calls %v", handled, cmd != nil, q.calls)
	}
	cmd, ok := m.sidebarAgentUndoDrop("here", "w-2")
	if !ok || cmd == nil {
		t.Fatal("the person's u after a refused one did not undo the drop")
	}
	runMsg(t, m, cmd)
	if p, _ := q.last("queue-prompt"); p["text"] != "put back" || p["human_nonce"] != "nonce-1" {
		t.Errorf("the undo queued %v", p)
	}
}

// forgetSidebarState removes the sidebar.json a test's focus changes write,
// so the seen marks it left do not reach the next test in the binary.
func forgetSidebarState(t *testing.T) {
	t.Helper()
	path := filepath.Join(sidebarStateDir(), sidebarStateFileName)
	_ = os.Remove(path)
	t.Cleanup(func() { _ = os.Remove(path) })
}
