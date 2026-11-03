package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// printedRead runs one read-agent-messages answer through the printer.
func printedRead(t *testing.T, answer map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := printAgentMessages(&buf, raw, ""); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// TestAnInboxReadIsToldOnlyOfItsOwnLostMail pins the line an agent acts on.
// The ring is the session's, so its count of drops grows with everyone's
// mail. Printed in every inbox, it told every agent that mail it never had
// was lost, and the agent that did lose some could not tell.
func TestAnInboxReadIsToldOnlyOfItsOwnLostMail(t *testing.T) {
	one := []map[string]any{{"id": 270, "kind": "message", "from_label": "review", "text": "retested"}}

	out := printedRead(t, map[string]any{"inbox": "alice", "messages": one, "total": 1, "evicted": 14, "evicted_unread": 0})
	if strings.Contains(out, "dropped") {
		t.Errorf("an inbox that lost nothing is told something was dropped:\n%s", out)
	}

	out = printedRead(t, map[string]any{"inbox": "alice", "messages": one, "total": 1, "evicted": 14, "evicted_unread": 2})
	if want := "2 messages to this inbox were dropped before they were read: the ring was full."; !strings.Contains(out, want) {
		t.Errorf("an inbox that lost two unread messages is not told so, want %q in:\n%s", want, out)
	}
}

// TestAnEmptyInboxSaysItsMailWasDropped is the read that most needs the line:
// when the only unread message was dropped, the inbox reads back empty.
func TestAnEmptyInboxSaysItsMailWasDropped(t *testing.T) {
	out := printedRead(t, map[string]any{"inbox": "alice", "messages": []any{}, "total": 0, "evicted": 3, "evicted_unread": 1})
	for _, want := range []string{"No messages.", "1 message to this inbox was dropped before it was read: the ring was full."} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// TestASessionReadSaysWhatTheRingDropped: a read of the whole session is told
// about everything the ring dropped, and how much of it was unread mail.
func TestASessionReadSaysWhatTheRingDropped(t *testing.T) {
	one := []map[string]any{{"id": 270, "kind": "notice", "text": "deploying"}}
	cases := []struct {
		unread uint64
		want   string
	}{
		{0, "The ring was full and dropped 14 older entries, none of them unread mail."},
		{3, "The ring was full and dropped 14 older entries. 3 of them were unread mail."},
	}
	for _, c := range cases {
		out := printedRead(t, map[string]any{"messages": one, "total": 1, "evicted": 14, "evicted_unread": c.unread})
		if !strings.Contains(out, c.want) {
			t.Errorf("want %q in:\n%s", c.want, out)
		}
	}
}
