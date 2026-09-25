package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recordingAttention is a store whose events are kept for the test to read.
func recordingAttention() (*attentionStore, *[]streamEvent) {
	var events []streamEvent
	a := newAttentionStore(func(ev streamEvent) { events = append(events, ev) }, func() uint64 { return uint64(len(events)) })
	return a, &events
}

// agentEvent is an agent-state transition as the diff raises it.
func agentEvent(window, from, to, kind, message string, prevDone, done uint64) SessionEvent {
	return SessionEvent{
		Type:              EventAgentState,
		Window:            window,
		State:             to,
		hookPrevState:     from,
		hookTitle:         "agent " + window,
		hookWorkspace:     2,
		hookHarness:       "claude-code",
		hookMessage:       message,
		hookKind:          kind,
		completionSeq:     done,
		prevCompletionSeq: prevDone,
	}
}

func openItems(t *testing.T, a *attentionStore) []AttentionItem {
	t.Helper()
	items, _, _ := a.list(attentionQuery{})
	return items
}

func actions(events []streamEvent) string {
	var out []string
	for _, ev := range events {
		out = append(out, ev.Action+":"+ev.Attention.Kind)
	}
	return strings.Join(out, " ")
}

func TestAttentionFollowsAgentState(t *testing.T) {
	a, events := recordingAttention()

	a.noteSessionEvent("work", agentEvent("w1", "working", "needs_input", "approval", "approve Bash: go test ./...", 0, 0))
	items := openItems(t, a)
	if len(items) != 1 || items[0].Kind != AttentionApproval {
		t.Fatalf("needs_input with blocked_by approval opened %+v, want one approval", items)
	}
	it := items[0]
	if it.Session != "work" || it.Window != "w1" || it.Workspace != 2 || it.Harness != "claude-code" || it.Name != "agent w1" || it.Summary != "approve Bash: go test ./..." {
		t.Errorf("the item does not describe the pane: %+v", it)
	}

	// The same block reported again is not news.
	a.noteSessionEvent("work", agentEvent("w1", "working", "needs_input", "approval", "approve Bash: go test ./...", 0, 0))
	if got := actions(*events); got != "open:approval" {
		t.Errorf("a repeated block published %q, want only the open", got)
	}

	// Leaving needs_input resolves it.
	a.noteSessionEvent("work", agentEvent("w1", "needs_input", "working", "", "", 0, 0))
	if items := openItems(t, a); len(items) != 0 {
		t.Fatalf("working left %+v open", items)
	}
	last := (*events)[len(*events)-1]
	if last.Action != AttentionClosed || last.Attention.Closed != AttentionClosedResolved {
		t.Errorf("the close said %s/%s, want close/resolved", last.Action, last.Attention.Closed)
	}

	// A question, then an error, then a finished turn.
	a.noteSessionEvent("work", agentEvent("w1", "working", "needs_input", "question", "which branch?", 0, 0))
	if items := openItems(t, a); len(items) != 1 || items[0].Kind != AttentionQuestion {
		t.Fatalf("a question opened %+v", items)
	}
	a.noteSessionEvent("work", agentEvent("w1", "needs_input", "errored", "", "rate limited", 0, 0))
	if items := openItems(t, a); len(items) != 1 || items[0].Kind != AttentionErrored || items[0].Summary != "rate limited" {
		t.Fatalf("errored left %+v", items)
	}
	a.noteSessionEvent("work", agentEvent("w1", "errored", "working", "", "", 0, 0))
	a.noteSessionEvent("work", agentEvent("w1", "working", "idle", "", "all tests pass", 0, 1))
	items = openItems(t, a)
	if len(items) != 1 || items[0].Kind != AttentionFinished || items[0].CompletionSeq != 1 {
		t.Fatalf("a finished turn opened %+v", items)
	}

	// Coming to rest without a counted turn opens nothing.
	a.noteSessionEvent("work", agentEvent("w2", "working", "idle", "", "", 3, 3))
	if items := openItems(t, a); len(items) != 1 {
		t.Errorf("a pane at rest with no finished turn opened an item: %+v", items)
	}

	// A new turn supersedes the finished one.
	a.noteSessionEvent("work", agentEvent("w1", "idle", "working", "", "", 1, 1))
	if items := openItems(t, a); len(items) != 0 {
		t.Errorf("a new turn left %+v open", items)
	}
}

func TestAttentionFinishedClosesWhenSeen(t *testing.T) {
	a, _ := recordingAttention()
	a.noteSessionEvent("work", agentEvent("w1", "working", "done", "", "", 2, 3))

	// Seen at an older count is not this turn.
	a.noteSessionEvent("work", SessionEvent{Type: eventCompletionSeen, Window: "w1", completionSeq: 2})
	if items := openItems(t, a); len(items) != 1 {
		t.Fatalf("a look at turn 2 closed the item for turn 3")
	}
	a.noteSessionEvent("work", SessionEvent{Type: eventCompletionSeen, Window: "w1", completionSeq: 3})
	if items := openItems(t, a); len(items) != 0 {
		t.Fatalf("focusing the pane left %+v open", items)
	}
}

func TestAttentionClosesWithWindowAndSession(t *testing.T) {
	a, _ := recordingAttention()
	a.noteSessionEvent("work", agentEvent("w1", "working", "errored", "", "boom", 0, 0))
	a.noteSessionEvent("work", agentEvent("w2", "working", "needs_input", "", "ok?", 0, 0))
	a.noteSessionEvent("other", agentEvent("w1", "working", "needs_input", "", "ok?", 0, 0))

	a.noteSessionEvent("work", SessionEvent{Type: EventWindowClosed, Window: "w1"})
	if items := openItems(t, a); len(items) != 2 {
		t.Fatalf("closing w1 in work left %d items, want 2", len(items))
	}
	a.closeSession("work")
	items := openItems(t, a)
	if len(items) != 1 || items[0].Session != "other" {
		t.Fatalf("ending work left %+v, want only other's item", items)
	}
}

// TestAttentionAFanDoesNotStorm is the sixteen-way fan: every agent finishing
// at once, then the same transitions arriving again. Each pane is one item and
// one event, and the repeats publish nothing.
func TestAttentionAFanDoesNotStorm(t *testing.T) {
	a, events := recordingAttention()
	for round := range 2 {
		for i := range 16 {
			w := fmt.Sprintf("w%d", i)
			a.noteSessionEvent(fmt.Sprintf("fan-%d", i), agentEvent(w, "working", "needs_input", "approval", "approve Bash", 0, 0))
		}
		if got := len(*events); got != 16 {
			t.Fatalf("round %d: %d events, want 16", round, got)
		}
	}
	if items := openItems(t, a); len(items) != 16 {
		t.Fatalf("%d items, want 16", len(items))
	}
}

func TestAttentionMailOneItemPerThread(t *testing.T) {
	a, events := recordingAttention()
	msg := AgentMessage{Kind: agentMsgDirect, Session: "s", From: "w1", FromLabel: "planner", To: AgentInboxHuman, Text: "first line\nsecond", ThreadID: 4}
	a.noteMail(msg)
	a.noteMail(msg)
	items := openItems(t, a)
	if len(items) != 1 || items[0].Count != 2 || items[0].Summary != "first line" || items[0].Name != "planner" || items[0].Thread != 4 {
		t.Fatalf("two messages in one thread gave %+v", items)
	}

	// Mail between agents, a notice and the person's own reply are not for the Inbox.
	a.noteMail(AgentMessage{Kind: agentMsgDirect, Session: "s", From: "w1", To: "w2", ThreadID: 9})
	a.noteMail(AgentMessage{Kind: agentMsgNotice, Session: "s", From: "w1", ThreadID: 10})
	a.noteMail(AgentMessage{Kind: agentMsgDirect, Session: "s", From: AgentInboxHuman, To: AgentInboxHuman, ThreadID: 11})
	if items := openItems(t, a); len(items) != 1 {
		t.Fatalf("mail not addressed to the person opened items: %+v", items)
	}

	a.noteMailRead("s", 4)
	if items := openItems(t, a); len(items) != 0 {
		t.Fatalf("reading the thread left %+v", items)
	}
	if got := actions(*events); got != "open:mail update:mail close:mail" {
		t.Errorf("events %q", got)
	}
}

func TestAttentionTextIsOneSafeLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"run\n  go test\t./...", "run go test ./..."},
		{"\x1b[31mred\x1b[0m", "[31mred[0m"},
		{"curl -H 'Authorization: Bearer abc.def' x", "curl -H 'Authorization: Bearer [redacted]' x"},
		{"export GITHUB_TOKEN=ghp_123 && make", "export GITHUB_TOKEN=[redacted] && make"},
		{"password: hunter2", "password: [redacted]"},
		{"api_key=\"k 1\" ok", "api_key=[redacted] ok"},
		{"nothing secret here", "nothing secret here"},
	}
	for _, c := range cases {
		if got := attentionText(c.in, attentionMaxSummary); got != c.want {
			t.Errorf("attentionText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := strings.Repeat("é", 200)
	got := attentionText(long, attentionMaxSummary)
	if len(got) > attentionMaxSummary || !strings.HasPrefix(long, got) {
		t.Errorf("a long summary was cut to %d bytes, or mid-rune", len(got))
	}
}

func TestAttentionSurvivesARestartWithinReason(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "attention", "items.json")

	a, _ := recordingAttention()
	a.load(path, func(string, string) bool { return true })
	a.noteSessionEvent("work", agentEvent("w1", "working", "idle", "", "done", 0, 1))
	a.noteSessionEvent("work", agentEvent("w2", "working", "errored", "", "boom", 0, 0))
	a.noteSessionEvent("work", agentEvent("w3", "working", "needs_input", "approval", "ok?", 0, 0))
	a.noteSessionEvent("gone", agentEvent("w1", "working", "errored", "", "", 0, 0))
	a.noteSessionEvent("work", agentEvent("dead", "working", "errored", "", "", 0, 0))
	a.noteMail(AgentMessage{Kind: agentMsgDirect, Session: "work", From: "w1", To: AgentInboxHuman, Text: "hi", ThreadID: 3})
	a.saveNowAndFreeze()

	// Frozen: a pane closing during shutdown does not reach the file.
	a.noteSessionEvent("work", SessionEvent{Type: EventWindowClosed, Window: "w1"})
	time.Sleep(2 * attentionSaveDelay)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the queue was not saved: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the saved queue has mode %o, want 600", perm)
	}

	b, events := recordingAttention()
	live := func(session, window string) bool {
		return session == "work" && window != "dead"
	}
	b.load(path, live)
	got := map[string]bool{}
	for _, it := range openItems(t, b) {
		got[it.Kind+"/"+it.Window] = true
	}
	// Mail is dropped: its thread id points into a ring that did not survive
	// and would be reused by the next thread.
	want := map[string]bool{"finished/w1": true, "errored/w2": true}
	if len(got) != len(want) {
		t.Errorf("after a restart the queue holds %v, want %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("after a restart %s is missing", k)
		}
	}
	if len(*events) != 0 {
		t.Errorf("loading published %d events", len(*events))
	}

	// Ids carry on rather than being reused.
	b.noteSessionEvent("work", agentEvent("w4", "working", "errored", "", "", 0, 0))
	for _, it := range openItems(t, b) {
		if it.Window == "w4" && it.ID != "7" {
			t.Errorf("the first item after a restart has id %s, want 7", it.ID)
		}
	}
}

func TestAttentionSaveIsDebounced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "attention", "items.json")
	a, _ := recordingAttention()
	a.load(path, func(string, string) bool { return true })
	for i := range 20 {
		a.noteSessionEvent("work", agentEvent(fmt.Sprintf("w%d", i), "working", "errored", "", "", 0, 0))
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			var f attentionFile
			if json.Unmarshal(data, &f) == nil && len(f.Items) == 20 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the queue was not saved after a burst of changes")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// savedAttention writes a queue with an errored item on w1 and a finished
// item on w2, in session work, and returns its path.
func savedAttention(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "attention", "items.json")
	a, _ := recordingAttention()
	a.load(path, func(string, string) bool { return true })
	a.noteSessionEvent("work", agentEvent("w1", "working", "errored", "", "boom", 0, 0))
	a.noteSessionEvent("work", agentEvent("w2", "working", "idle", "", "done", 0, 1))
	a.saveNowAndFreeze()
	return path
}

// A restored pane can exit while the Inbox loads. Its exit reaches the store
// through the event sink while the sink's caller holds the session's state
// lock, and live takes that same lock. load must not hold mu across live.
func TestAttentionLoadDoesNotHoldTheLockAcrossLive(t *testing.T) {
	path := savedAttention(t)
	b, _ := recordingAttention()
	var blocked bool
	live := func(string, string) bool {
		done := make(chan struct{})
		go func() {
			b.noteSessionEvent("work", SessionEvent{Type: EventWindowClosed, Window: "other"})
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			blocked = true
		}
		return true
	}
	b.load(path, live)
	if blocked {
		t.Fatal("an event during load waited on the store lock that load held across live")
	}
}

// An item opened before load keeps its id, and a saved item with the same id
// gets a fresh one, so the two never share an id and byKey stays right.
func TestAttentionLoadGivesACollidingSavedItemAFreshID(t *testing.T) {
	path := savedAttention(t)
	b, _ := recordingAttention()
	b.noteSessionEvent("work", agentEvent("w9", "working", "errored", "", "early", 0, 0))
	early := openItems(t, b)
	if len(early) != 1 {
		t.Fatalf("before load the queue holds %d items, want 1", len(early))
	}
	b.load(path, func(string, string) bool { return true })

	items := openItems(t, b)
	if len(items) != 3 {
		t.Fatalf("after load the queue holds %d items, want 3", len(items))
	}
	ids := map[string]bool{}
	for _, it := range items {
		if ids[it.ID] {
			t.Errorf("id %s is held by two items", it.ID)
		}
		ids[it.ID] = true
		if it.Window == "w9" && it.ID != early[0].ID {
			t.Errorf("the item opened before load changed id from %s to %s", early[0].ID, it.ID)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for key, id := range b.byKey {
		it, ok := b.items[id]
		if !ok || attentionKey(it.Kind, it.Session, it.Window, it.Thread) != key {
			t.Errorf("byKey[%q] = %s, which is not the item with that key", key, id)
		}
	}
}
