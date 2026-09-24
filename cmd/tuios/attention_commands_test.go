package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPrintAttentionListGroupsAndCleans(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	at := func(ago time.Duration) int64 { return now.Add(-ago).UnixNano() }
	raw, _ := json.Marshal(map[string]any{"items": []map[string]any{
		{"id": "3", "kind": "approval", "session": "fan-1", "name": "claude", "summary": "approve Bash: go test", "since": at(12 * time.Minute)},
		{"id": "4", "kind": "question", "session": "fan-2", "window": "0123456789abcdef", "summary": "which\x1b[31m branch?", "since": at(40 * time.Second)},
		{"id": "9", "kind": "mail", "session": "work", "name": "planner", "summary": "ready?", "count": 3, "since": at(3 * time.Hour)},
	}})
	var out bytes.Buffer
	if err := printAttentionList(&out, raw, now); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"Approvals\n   12m  #3     fan-1/claude  approve Bash: go test\n",
		"Questions\n   40s  #4     fan-2/01234567  which[31m branch?\n",
		"Mail\n    3h  #9     work/planner  ready? (3)\n",
		"3 waiting.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("an escape reached the terminal: %q", got)
	}
}

// TestPrintAttentionListHeadingsMatchTheInbox: an ask-human question and an
// agent's question share the Questions heading, and a finished turn is under
// Done, as in the TUI's Inbox. They were "Asked you" and "Finished".
func TestPrintAttentionListHeadingsMatchTheInbox(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	raw, _ := json.Marshal(map[string]any{"items": []map[string]any{
		{"id": "1", "kind": "ask", "session": "a", "name": "script", "summary": "deploy?", "since": now.UnixNano()},
		{"id": "2", "kind": "question", "session": "a", "name": "claude", "summary": "which branch?", "since": now.UnixNano()},
		{"id": "3", "kind": "finished", "session": "a", "name": "codex", "summary": "", "since": now.UnixNano()},
	}})
	var out bytes.Buffer
	if err := printAttentionList(&out, raw, now); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Count(got, "Questions\n") != 1 || !strings.Contains(got, "Done\n") {
		t.Errorf("want one Questions heading and a Done heading:\n%s", got)
	}
	if strings.Contains(got, "Asked you") || strings.Contains(got, "Finished") {
		t.Errorf("an old heading is back:\n%s", got)
	}
}

// TestPrintAttentionListSaysAnApprovalIsHeld: a held approval's pane shows no
// prompt, so the row says where to answer it, and an approval nothing holds
// says nothing more.
func TestPrintAttentionListSaysAnApprovalIsHeld(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	raw, _ := json.Marshal(map[string]any{"items": []map[string]any{
		{"id": "3", "kind": "approval", "session": "fan-1", "name": "claude", "summary": "approve Bash: go test", "since": now.UnixNano(), "request_id": "9f86d081884c7d65"},
		{"id": "5", "kind": "approval", "session": "fan-2", "name": "codex", "summary": "approve Bash: rm", "since": now.UnixNano()},
	}})
	var out bytes.Buffer
	if err := printAttentionList(&out, raw, now); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "fan-1/claude  approve Bash: go test  (held: answer in the Inbox)\n") {
		t.Errorf("the held approval does not say so:\n%s", got)
	}
	if !strings.Contains(got, "fan-2/codex  approve Bash: rm\n") {
		t.Errorf("an approval nothing holds says more than its summary:\n%s", got)
	}
}

// TestPrintAttentionListNamesTheMachine: an item of a linked host is named
// host:session, and one of a host whose link is down says so, with when it was
// last heard from.
//
// Negative control: without the stale branch in printAttentionList the row
// reads like a live one.
func TestPrintAttentionListNamesTheMachine(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	at := func(ago time.Duration) int64 { return now.Add(-ago).UnixNano() }
	raw, _ := json.Marshal(map[string]any{"items": []map[string]any{
		{"id": "build:3", "kind": "approval", "host": "build", "session": "api", "name": "claude", "summary": "approve Bash", "since": at(time.Minute)},
		{"id": "pi:1", "kind": "errored", "host": "pi", "session": "lab", "name": "codex", "since": at(time.Hour), "stale": true, "seen_at": at(5 * time.Minute)},
	}})
	var out bytes.Buffer
	if err := printAttentionList(&out, raw, now); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"build:api/claude  approve Bash\n", "pi:lab/codex  [unreachable, seen 5m ago]\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
}

func TestPrintAttentionListEmpty(t *testing.T) {
	var out bytes.Buffer
	if err := printAttentionList(&out, json.RawMessage(`{"items":[]}`), time.Now()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Nothing is waiting") {
		t.Errorf("empty Inbox printed %q", out.String())
	}
}
