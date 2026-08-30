package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestPrintedMessagesShowTheirThread holds the printed shape of a threaded
// listing. A reader scanning mail has to be able to see that one message answers
// another without opening the JSON, and an unthreaded message has to read
// exactly as it did before: a thread id that is the message's own id is noise,
// not information.
func TestPrintedMessagesShowTheirThread(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"messages": []map[string]any{
			{"id": 1, "kind": "message", "from_label": "orchestrator", "text": "please retest", "thread_id": 1},
			{"id": 2, "kind": "message", "from_label": "build", "text": "retested, green", "reply_to": 1, "thread_id": 1},
			{"id": 3, "kind": "message", "from_label": "build", "text": "answering something gone", "reply_to": 9, "thread_id": 9, "reply_to_missing": true},
		},
		"total": 3, "unread": 0, "thread": 1,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var buf bytes.Buffer
	if err := printAgentMessages(&buf, raw); err != nil {
		t.Fatalf("printAgentMessages: %v", err)
	}
	out := buf.String()
	lines := strings.Split(out, "\n")

	first := lines[0]
	if strings.Contains(first, "thread") || strings.Contains(first, "reply to") {
		t.Errorf("a message that starts a thread named one anyway: %q", first)
	}
	if !strings.Contains(out, "reply to #1") {
		t.Errorf("a reply did not say what it answers:\n%s", out)
	}
	if !strings.Contains(out, "thread #1") {
		t.Errorf("a reply did not name its thread:\n%s", out)
	}
	if !strings.Contains(out, "the message this answers has been dropped from the ring") {
		t.Errorf("a reply to an evicted message said nothing about it:\n%s", out)
	}
	if !strings.Contains(out, "3 message(s) in thread 1") {
		t.Errorf("the summary did not say which thread was read:\n%s", out)
	}
}

// TestAnEmptyThreadSaysWhy covers the answer a filter gives when nothing is
// left. "No messages." would be true and useless: the caller asked about one
// conversation, and the reason it is empty is the ring, not the filter.
func TestAnEmptyThreadSaysWhy(t *testing.T) {
	raw, err := json.Marshal(map[string]any{"messages": []map[string]any{}, "total": 0, "unread": 0, "thread": 7})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var buf bytes.Buffer
	if err := printAgentMessages(&buf, raw); err != nil {
		t.Fatalf("printAgentMessages: %v", err)
	}
	if !strings.Contains(buf.String(), "No messages in thread 7") {
		t.Errorf("an empty thread did not say which thread was empty: %q", buf.String())
	}
}
