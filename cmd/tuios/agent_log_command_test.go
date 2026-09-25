package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestAgentLogCleansWhatItPrints: the activity ring holds text an agent
// wrote, so agent-log and its recap reach the terminal with its control
// characters left out.
func TestAgentLogCleansWhatItPrints(t *testing.T) {
	at := time.Date(2026, 9, 24, 14, 2, 11, 0, time.Local).UnixNano()
	raw, _ := json.Marshal(map[string]any{"entries": []map[string]any{
		{"seq": 1, "at": at, "kind": "prompt", "text": "fix\x1b[2J it"},
		{"seq": 2, "at": at, "kind": "tool_failed", "tool": "Ba\x1bsh", "target": "go\x07 test", "text": "Exit\x1b]0;x\x07 1", "ok": false},
		{"seq": 3, "at": at, "kind": "tool_done", "tool": "apply_patch", "target": "a.go", "files": []string{"a\x1b[31m.go"}},
	}})
	var out bytes.Buffer
	if err := printAgentLog(&out, raw); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\x07") {
		t.Errorf("a control character reached the log output: %q", out.String())
	}

	now := time.Unix(0, at).Add(42 * time.Minute)
	raw, _ = json.Marshal(map[string]any{"entries": []any{}, "recap": map[string]any{
		"since": at, "turns": 1, "files": []string{"a\x1b[2J.go"}, "files_total": 1, "commands": 1,
		"tests":     map[string]any{"cmdline": "go test\x1b]0;x\x07", "ok": true, "at": now.UnixNano()},
		"last_said": "done\x1b[2J", "state": "working",
	}})
	out.Reset()
	if err := printAgentRecap(&out, raw, now); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\x07") {
		t.Errorf("a control character reached the recap output: %q", out.String())
	}
}
