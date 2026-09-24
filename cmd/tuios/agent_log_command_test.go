package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestAgentLogReadsWhatTheHooksReported runs the whole path against a real
// daemon: Claude Code hook payloads through tuios agent-hook, then tuios
// agent-log reading the ring back and summarising it.
func TestAgentLogReadsWhatTheHooksReported(t *testing.T) {
	c := startSubscribeDaemon(t)
	if _, err := c.Call("new-session", map[string]any{"name": "work", "window_name": "api"}); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	raw, err := c.Call("list-windows", map[string]any{"session": "work"})
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	var windows struct {
		Windows []struct {
			ID string `json:"window_id"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(raw, &windows); err != nil || len(windows.Windows) != 1 {
		t.Fatalf("list-windows = %s (%v)", raw, err)
	}
	pane := windows.Windows[0].ID

	hook := func(payload string) {
		t.Helper()
		var stderr bytes.Buffer
		runAgentHook(agentHookOptions{explain: true, timeout: 5 * time.Second}, []string{"claude-code"}, agentHookIO{
			stdin:  strings.NewReader(payload),
			stdout: &bytes.Buffer{},
			stderr: &stderr,
			getenv: func(k string) string {
				return map[string]string{"TUIOS_PANE_ID": pane, "TUIOS_SESSION": "work"}[k]
			},
			dial: func() (verbCaller, error) { return session.DialVerbClientAs("test") },
		})
		if !strings.Contains(stderr.String(), `"activity_recorded":true`) {
			t.Fatalf("%s: %s", payload, stderr.String())
		}
	}
	hook(`{"hook_event_name":"UserPromptSubmit","session_id":"s1","prompt":"make the retry configurable"}`)
	hook(`{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Edit","tool_input":{"file_path":"api/retry.go"}}`)
	hook(`{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"Edit","tool_input":{"file_path":"api/retry.go"}}`)
	hook(`{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Bash","tool_input":{"command":"go test ./api/"}}`)
	hook(`{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"Bash","tool_input":{"command":"go test ./api/"}}`)
	hook(`{"hook_event_name":"Stop","session_id":"s1","last_assistant_message":"Made the retry configurable.\nDetails below."}`)

	var out bytes.Buffer
	if err := runAgentLog(agentLogOptions{session: "work", window: pane, limit: 64}, &out); err != nil {
		t.Fatalf("agent-log: %v", err)
	}
	log := out.String()
	for _, want := range []string{
		"prompt    make the retry configurable",
		"tool      Edit: api/retry.go",
		"done      Edit: api/retry.go  (wrote api/retry.go)",
		"tool      Bash: go test ./api/",
		"said      Made the retry configurable.",
		"state     done",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("agent-log lacks %q:\n%s", want, log)
		}
	}

	out.Reset()
	if err := runAgentLog(agentLogOptions{session: "work", window: pane, limit: 64, recap: true, since: time.Hour}, &out); err != nil {
		t.Fatalf("agent-log --recap: %v", err)
	}
	recap := out.String()
	for _, want := range []string{
		"1 turn. 1 file: api/retry.go\n",
		"1 command. Tests: go test ./api/ passed",
		"Last said: Made the retry configurable.\n",
		"Now: done\n",
	} {
		if !strings.Contains(recap, want) {
			t.Errorf("agent-log --recap lacks %q:\n%s", want, recap)
		}
	}

	// The rail's line for the pane: now cleared at the end of the turn, the
	// prompt kept.
	raw, err = c.Call("get-agent-state", map[string]any{"session": "work", "window": pane})
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Message string            `json:"message"`
		Meta    map[string]string `json:"meta"`
	}
	_ = json.Unmarshal(raw, &st)
	if st.Meta["prompt"] != "make the retry configurable" || st.Meta["now"] != "" || st.Message != "Made the retry configurable." {
		t.Errorf("get-agent-state = %s", raw)
	}
}

// TestAgentLogPrinting holds the two outputs to their shape: one plain line
// per entry, and a recap that says what it does not know.
func TestAgentLogPrinting(t *testing.T) {
	at := time.Date(2026, 9, 24, 14, 2, 11, 0, time.Local).UnixNano()
	raw, _ := json.Marshal(map[string]any{"entries": []map[string]any{
		{"seq": 1, "at": at, "kind": "prompt", "text": "fix\x1b[2J it"},
		{"seq": 2, "at": at, "kind": "tool_failed", "tool": "Bash", "target": "go test ./...", "text": "Exit code 1", "ok": false},
		{"seq": 3, "at": at, "kind": "tool_done", "tool": "apply_patch", "target": "a.go", "files": []string{"a.go", "b.go", "c.go", "d.go"}},
		{"seq": 4, "at": at, "kind": "command", "target": "make lint", "exit": 2},
	}})
	var out bytes.Buffer
	if err := printAgentLog(&out, raw); err != nil {
		t.Fatal(err)
	}
	want := "14:02:11  prompt    fix[2J it\n" +
		"14:02:11  failed    Bash: go test ./...  Exit code 1\n" +
		"14:02:11  done      apply_patch: a.go  (wrote a.go, b.go, c.go and 1 more)\n" +
		"14:02:11  command   make lint  (exit 2)\n"
	if out.String() != want {
		t.Errorf("printAgentLog =\n%s\nwant\n%s", out.String(), want)
	}

	out.Reset()
	_ = printAgentLog(&out, json.RawMessage(`{"entries":[]}`))
	if !strings.Contains(out.String(), "No activity recorded") {
		t.Errorf("an empty ring printed %q", out.String())
	}

	now := time.Unix(0, at).Add(42 * time.Minute)
	raw, _ = json.Marshal(map[string]any{"entries": []any{}, "recap": map[string]any{
		"since": at, "turns": 3, "files": []string{"a.go", "b.go", "c.go", "d.go"}, "files_total": 6, "commands": 11,
		"tests": map[string]any{"cmdline": "go test ./...", "ok": nil, "at": now.Add(-2 * time.Minute).UnixNano()},
		"state": "working",
	}})
	out.Reset()
	if err := printAgentRecap(&out, raw, now); err != nil {
		t.Fatal(err)
	}
	want = "Since 14:02 (42m ago)\n" +
		"3 turns. 6 files: a.go, b.go, c.go and 3 more\n" +
		"11 commands. Tests: go test ./... ran 2m ago, and nothing said how\n" +
		"Now: working\n"
	if out.String() != want {
		t.Errorf("printAgentRecap =\n%s\nwant\n%s", out.String(), want)
	}
}

// TestAgentLogParams: --since becomes a time, and the flags are checked
// before anything is dialed.
func TestAgentLogParams(t *testing.T) {
	now := time.Unix(1000, 0)
	p, err := agentLogParams(agentLogOptions{window: "w", since: 30 * time.Minute, limit: 10, recap: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if p["since"] != now.Add(-30*time.Minute).UnixNano() || p["limit"] != 10 || p["recap"] != true || p["window"] != "w" {
		t.Errorf("params = %v", p)
	}
	if p, _ := agentLogParams(agentLogOptions{limit: 64}, now); p["since"] != nil || p["recap"] != nil {
		t.Errorf("defaults sent %v", p)
	}
	for _, o := range []agentLogOptions{{limit: 0}, {limit: 257}, {limit: 5, since: -time.Minute}} {
		if _, err := agentLogParams(o, now); err == nil {
			t.Errorf("%+v was accepted", o)
		}
	}
}
