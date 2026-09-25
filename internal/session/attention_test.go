package session

import (
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
