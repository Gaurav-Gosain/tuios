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
