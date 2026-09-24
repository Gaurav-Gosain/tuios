package input

import (
	"encoding/json"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestAgentRowXFallsThroughWithNothingQueued: x on an agent row drops a queued
// message only when one waits; on a row with none it opens the rail's own
// menu, as it did before the agent rows had keys.
func TestAgentRowXFallsThroughWithNothingQueued(t *testing.T) {
	o := reachAgentsOS(t)
	o.Windows[0].AgentQueued = 0
	o.Mode = app.WindowManagementMode
	o = pressKey(t, o, "x")
	got := o.RecentActions()
	rail := o.KeybindRegistry.GetSidebarAction("x")
	if len(got) == 0 || got[len(got)-1] != rail {
		t.Fatalf("x on a row with nothing queued ran %v, want the rail's %s", got, rail)
	}
	for _, a := range got {
		if a == config.ActionAgentCancelQueued {
			t.Fatalf("x ran %s with nothing queued", a)
		}
	}
}

// TestInboxReplyLineTakesEveryKey: while the reply line is open every
// printable key is text, including the Inbox's own keys, and esc closes it.
func TestInboxReplyLineTakesEveryKey(t *testing.T) {
	o := railOS(t)
	o.Windows[0].AgentState = "working"
	o.SetInboxVerbCaller(func(string, map[string]any, time.Duration) (json.RawMessage, error) { return nil, nil }, func() string { return "n" })
	if _, handled := o.SidebarAgentReply("", o.Windows[0].ID); !handled || !o.InboxReplyOpen() {
		t.Fatal("the reply line did not open")
	}
	for _, k := range []string{"d", "q", "z", "space", "j"} {
		o = pressKey(t, o, k)
	}
	if !o.InboxReplyOpen() || !o.ShowInbox {
		t.Fatal("an Inbox key closed the reply line")
	}
	o = pressKey(t, o, "esc")
	if o.InboxReplyOpen() || o.ShowInbox {
		t.Error("esc left the reply line or the Inbox the rail opened")
	}
}

// queueRecorder answers the queue verbs the reply keys send and records them.
type queueRecorder struct {
	calls   []map[string]any
	verbs   []string
	entries []map[string]any
}

func (q *queueRecorder) call(verb string, params map[string]any, _ time.Duration) (json.RawMessage, error) {
	q.verbs = append(q.verbs, verb)
	q.calls = append(q.calls, params)
	switch verb {
	case "queue-prompt":
		id := "q" + string(rune('1'+len(q.entries)))
		q.entries = append(q.entries, map[string]any{"id": id, "state": "waiting"})
		return json.Marshal(map[string]any{"id": id, "position": len(q.entries), "queued": len(q.entries)})
	case "list-queued":
		return json.Marshal(map[string]any{"entries": q.entries})
	case "cancel-queued":
		q.entries = q.entries[:len(q.entries)-1]
		return json.Marshal(map[string]any{"queued": len(q.entries)})
	}
	return json.Marshal(map[string]any{})
}

// count is how many times verb was sent.
func (q *queueRecorder) count(verb string) int {
	n := 0
	for _, v := range q.verbs {
		if v == verb {
			n++
		}
	}
	return n
}

// lastText is the text of the last queue-prompt.
func (q *queueRecorder) lastText() string {
	for i := len(q.verbs) - 1; i >= 0; i-- {
		if q.verbs[i] == "queue-prompt" {
			s, _ := q.calls[i]["text"].(string)
			return s
		}
	}
	return ""
}

// pressRun presses a key and runs the command it returns, handing its message
// back to the model, as the program would.
func pressRun(t *testing.T, o *app.OS, spec string) *app.OS {
	t.Helper()
	o, cmd := HandleKeyPress(keyEventFor(t, spec), o)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			o.Update(msg)
		}
	}
	return o
}

// replyRowOS is the rail's cursor on an agent row with a recording queue.
func replyRowOS(t *testing.T) (*app.OS, *queueRecorder) {
	t.Helper()
	o := reachAgentsOS(t)
	o.Windows[0].AgentState = "working"
	o.Windows[0].AgentQueued = 0
	o.Mode = app.WindowManagementMode
	q := &queueRecorder{}
	o.SetInboxVerbCaller(q.call, func() string { return "nonce-1" })
	return o, q
}

// TestAgentRowUUndoesTheDrop: u on the rail's agent row, a moment after x
// dropped a message this client queued, queues its text again rather than
// marking the row unread. A u from send-keys does not, since the undo queues
// as the person.
func TestAgentRowUUndoesTheDrop(t *testing.T) {
	o, q := replyRowOS(t)
	o = pressRun(t, o, "r")
	if !o.InboxReplyOpen() {
		t.Fatal("r on the agent row did not open the reply line")
	}
	for _, k := range []string{"h", "i"} {
		o = pressRun(t, o, k)
	}
	o = pressRun(t, o, "enter")
	if q.count("queue-prompt") != 1 || q.lastText() != "hi" {
		t.Fatalf("the reply queued %v", q.calls)
	}
	o.Windows[0].AgentQueued = 1
	o = pressRun(t, o, "x")
	if q.count("cancel-queued") != 1 {
		t.Fatalf("x on the row sent %v", q.verbs)
	}

	o.ProcessingRemoteKeys = true
	o = pressRun(t, o, "u")
	o.ProcessingRemoteKeys = false
	if q.count("queue-prompt") != 1 {
		t.Fatal("a u from send-keys queued the dropped message as the person")
	}

	o = pressRun(t, o, "u")
	if q.count("queue-prompt") != 2 || q.lastText() != "hi" {
		t.Fatalf("u after the drop sent %v, want the text queued again", q.verbs)
	}
	for _, a := range o.RecentActions() {
		if a == config.ActionAgentUnread {
			return
		}
	}
	t.Error("u was not dispatched as the agent row's action")
}

// TestAgentRowXFromSendKeysDropsNothing: x from send-keys on a row with a
// queue cancels nothing, since a drop cancels as the person.
func TestAgentRowXFromSendKeysDropsNothing(t *testing.T) {
	o, q := replyRowOS(t)
	o.Windows[0].AgentQueued = 1
	q.entries = []map[string]any{{"id": "q9", "state": "waiting"}}
	o.ProcessingRemoteKeys = true
	o = pressRun(t, o, "x")
	o.ProcessingRemoteKeys = false
	if q.count("cancel-queued") != 0 || q.count("list-queued") != 0 {
		t.Fatalf("x from send-keys sent %v", q.verbs)
	}
	rail := o.KeybindRegistry.GetSidebarAction("x")
	if got := o.RecentActions(); len(got) > 0 && got[len(got)-1] == rail {
		t.Error("x from send-keys fell through to the rail's own action")
	}
}

// TestPasteIntoTheReplyLine: a paste while the reply line is open is the
// reply's text, one line of it, and never reaches the pane.
func TestPasteIntoTheReplyLine(t *testing.T) {
	o, q := replyRowOS(t)
	o = pressRun(t, o, "r")
	if !o.InboxReplyOpen() {
		t.Fatal("r on the agent row did not open the reply line")
	}
	next, _ := HandleInput(tea.PasteMsg{Content: "fix\n  the\ttests\n"}, o)
	o = next.(*app.OS)
	o = pressRun(t, o, "enter")
	if q.count("queue-prompt") != 1 || q.lastText() != "fix the tests" {
		t.Fatalf("the pasted reply queued %q (%v)", q.lastText(), q.verbs)
	}
}
