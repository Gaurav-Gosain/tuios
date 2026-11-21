package tuie2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func piFixture(t *testing.T, r *atRig) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "transcriptview", "testdata", "pi-session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(xdgDir(r.base, "HOME"), ".pi", "agent", "sessions", "--work-project--", "2026-10-22_pi-fixture.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func piHook(t *testing.T, r *atRig, payload map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(tuiosBin, "agent-hook", "pi", "--session", transcriptSession, "--window", r.wins[0], "--explain")
	cmd.Dir = workDirIn(t, r.base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, k := range xdgKeys {
		cmd.Env = append(cmd.Env, k+"="+xdgDir(r.base, k))
	}
	cmd.Stdin = bytes.NewReader(b)
	out, err := cmd.CombinedOutput()
	r.art.add("pi hook", string(out))
	if err != nil {
		t.Fatalf("Pi hook: %v\n%s", err, out)
	}
	var explanation map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out), &explanation); err != nil {
		t.Fatalf("Pi hook explanation: %v\n%s", err, out)
	}
	return explanation
}

func piTranscriptEvent(t *testing.T, r *atRig, sub *linkStream) map[string]any {
	t.Helper()
	sub.timeout = uiTimeout
	defer func() { sub.timeout = 0 }()
	for {
		line, err := sub.br.ReadString('\n')
		if err != nil {
			t.Fatalf("no Pi transcript event: %v", err)
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		r.art.add("Pi transcript event", line)
		if event["type"] == "transcript" && event["window"] == r.wins[0] {
			for key := range event {
				switch key {
				case "type", "session", "window", "cursor", "seq", "boot_id", "time":
				default:
					t.Fatalf("Pi transcript event exposed private metadata %q", key)
				}
			}
			return event
		}
	}
}

// A person reads a Pi session the hook joined: the branch that ends at its last
// entry, never an abandoned sibling or the model-only records, with the tool
// cards a reader expects. The session then grows and is paged through.
func TestPersonReadsPiTranscript(t *testing.T) {
	r := newATRig(t, "pi-transcript.txt", 1)
	path := piFixture(t, r)
	piHook(t, r, map[string]any{"hook_event_name": "session_start", "session_id": "pi-fixture", "transcript_path": path})
	entries := atEntries(t, r.read(r.wins[0], nil))
	encoded, _ := json.Marshal(entries)
	for _, forbidden := range []string{"ABANDONED-BRANCH", "PRIVATE-", "MODEL-ONLY-REPLACEMENT"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("Pi transcript exposed %q: %s", forbidden, encoded)
		}
	}
	for _, text := range []string{"Set b to three.", "[image]", "The value lives in app.ts.", "The edit is complete; tests failed.", "The turn stopped: interrupted", "Rate limit reached.", "b is three. One test fails."} {
		if !bytes.Contains(encoded, []byte(text)) {
			t.Errorf("Pi transcript is missing %q: %s", text, encoded)
		}
	}
	if strings.Count(string(encoded), "Set b to three.") != 1 {
		t.Errorf("the prompt was duplicated: %s", encoded)
	}
	var edited, written, failed bool
	for _, e := range entries {
		if e.Kind == "tool_call" && e.Tool == "edit" {
			edited = e.Status == "ok" && e.Diff != nil && e.Diff.File == "/work/project/app.ts" && e.Diff.Added == 1 && e.Diff.Removed == 1
		}
		if e.Kind == "tool_call" && e.Tool == "write" {
			written = e.Status == "ok" && e.Diff != nil && e.Diff.Added == 1
		}
		if e.Kind == "tool_call" && e.Tool == "bash" {
			failed = e.Status == "error" && e.Target == "npm test"
		}
	}
	if !edited || !written || !failed {
		t.Fatalf("Pi tool cards: edit=%v write=%v failed=%v\n%s", edited, written, failed, encoded)
	}

	sub := r.link.open(t, true)
	if _, err := sub.call(t, "subscribe", map[string]any{"session": transcriptSession, "types": []string{"transcript"}}); err != nil {
		t.Fatal(err)
	}
	appendLines(t, path, `{"type":"message","id":"later","parentId":"a3","message":{"role":"assistant","content":[{"type":"text","text":"APPENDED-LATER"}]}}`)
	if event := piTranscriptEvent(t, r, sub); event["cursor"] != nil {
		t.Fatalf("a Pi transcript event carried an offset cursor: %v", event)
	}
	if later, _ := json.Marshal(atEntries(t, r.read(r.wins[0], nil))); !bytes.Contains(later, []byte("APPENDED-LATER")) {
		t.Fatalf("an entry appended to the session is not shown: %s", later)
	}

	parent := "later"
	for _, tc := range []struct {
		id, command, output  string
		code                 int
		cancelled, truncated bool
	}{
		{"shellok", "echo direct", "DIRECT-SHELL-OK", 0, false, false},
		{"shellfail", "false", "DIRECT-SHELL-FAIL", 1, false, false},
		{"shellcancel", "sleep 99", "", 0, true, true},
	} {
		b, _ := json.Marshal(map[string]any{"type": "message", "id": tc.id, "parentId": parent, "message": map[string]any{"role": "bashExecution", "command": tc.command, "output": tc.output, "exitCode": tc.code, "cancelled": tc.cancelled, "truncated": tc.truncated, "excludeFromContext": true}})
		appendLines(t, path, string(b))
		parent = tc.id
	}
	var commands []string
	shell := atEntries(t, r.read(r.wins[0], nil))
	for _, e := range shell {
		if e.Kind == "tool_call" && e.Tool == "bash" && e.Target != "npm test" {
			commands = append(commands, e.Target+":"+e.Status)
		}
	}
	shellJSON, _ := json.Marshal(shell)
	if strings.Join(commands, "|") != "echo direct:ok|false:error|sleep 99:error" {
		t.Fatalf("direct shell statuses: %v", commands)
	}
	for _, text := range []string{"DIRECT-SHELL-OK", "DIRECT-SHELL-FAIL", "Command cancelled"} {
		if !bytes.Contains(shellJSON, []byte(text)) {
			t.Fatalf("direct shell history omitted %q: %s", text, shellJSON)
		}
	}

	appendRaw(t, path, `{"type":"message","id":"partial","parentId":"shellcancel","message":`)
	whole := r.read(r.wins[0], nil)
	var want, got []string
	for _, e := range atEntries(t, whole) {
		want = append(want, e.ID)
	}
	page := r.read(r.wins[0], map[string]any{"limit": 2})
	for n := 0; ; n++ {
		if n > 30 {
			t.Fatal("Pi paging did not terminate")
		}
		var ids []string
		for _, e := range atEntries(t, page) {
			ids = append(ids, e.ID)
		}
		got = append(ids, got...)
		older, _ := page["older"].(string)
		if older == "" {
			break
		}
		page = r.read(r.wins[0], map[string]any{"limit": 2, "before": older})
	}
	if len(want) == 0 || !slices.Equal(got, want) {
		t.Fatalf("Pi paged IDs %v, want exactly %v", got, want)
	}
	page = r.read(r.wins[0], map[string]any{"after": whole["cursor"].(string)})
	if page["reset"] != false || len(atEntries(t, page)) != 0 {
		t.Fatalf("Pi cursor consumed a partial line or duplicated entries: %v", page)
	}
}
