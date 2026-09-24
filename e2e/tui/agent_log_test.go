package tuie2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestAgentLogFromRealHooks drives the real binary end to end: Claude Code
// hook payloads through `tuios agent-hook`, exactly as the installed hooks run
// it, then `tuios agent-log` reading back what the daemon kept for the pane
// and summarising it with --recap. It also checks that set-agent-meta refuses
// the key the hooks now own.
//
// Negative control: against a binary without the activity ring, agent-log is
// an unknown subcommand and the first read fails; against a daemon that drops
// activity, the log is empty and every expected line is missing.
func TestAgentLogFromRealHooks(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-agent", "--detach"); err != nil {
		t.Fatalf("create the agent's session: %v\n%s", err, out)
	}

	hook := func(payload string) {
		t.Helper()
		cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "e2e-agent", "--window", "0", "--timeout", "10s")
		cmd.Dir = workDirIn(t, base)
		cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
		for _, key := range xdgKeys {
			cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
		}
		cmd.Stdin = strings.NewReader(payload)
		if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
			t.Fatalf("the hook for %s failed or printed: %v\n%s", payload, err, out)
		}
	}
	hook(`{"hook_event_name":"UserPromptSubmit","session_id":"e2e-log","prompt":"make the retry configurable"}`)
	hook(`{"hook_event_name":"PreToolUse","session_id":"e2e-log","tool_name":"Bash","tool_input":{"command":"go test ./api/"}}`)
	hook(`{"hook_event_name":"PostToolUseFailure","session_id":"e2e-log","tool_name":"Bash","tool_input":{"command":"go test ./api/"},"error":"Exit code 1"}`)
	hook(`{"hook_event_name":"PostToolUse","session_id":"e2e-log","tool_name":"Write","tool_input":{"file_path":"api/retry.go","content":"package api"}}`)
	hook(`{"hook_event_name":"Stop","session_id":"e2e-log","last_assistant_message":"The test still fails.\nHere is why."}`)

	out, err := tuiosCLI(t, base, "agent-log", "-s", "e2e-agent", "-w", "0")
	if err != nil {
		t.Fatalf("agent-log: %v\n%s", err, out)
	}
	for _, want := range []string{
		"prompt    make the retry configurable",
		"tool      Bash: go test ./api/",
		"failed    Bash: go test ./api/  Exit code 1",
		"done      Write: api/retry.go  (wrote api/retry.go)",
		"said      The test still fails.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("agent-log lacks %q:\n%s", want, out)
		}
	}

	out, err = tuiosCLI(t, base, "agent-log", "-s", "e2e-agent", "-w", "0", "--recap")
	if err != nil {
		t.Fatalf("agent-log --recap: %v\n%s", err, out)
	}
	for _, want := range []string{
		"1 turn. 1 file: api/retry.go",
		"1 command. Tests: go test ./api/ failed",
		"Last said: The test still fails.",
		"Now: done",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("agent-log --recap lacks %q:\n%s", want, out)
		}
	}

	if out, err := tuiosCLI(t, base, "set-agent-meta", "-s", "e2e-agent", "-w", "0", "now=typing"); err == nil {
		t.Errorf("set-agent-meta wrote the reserved key now:\n%s", out)
	}

	// A turn interrupted with Esc ends on an idle_prompt, which carries no
	// activity. The pane at rest must not keep showing the tool it ran.
	hook(`{"hook_event_name":"UserPromptSubmit","session_id":"e2e-log","prompt":"lint it"}`)
	hook(`{"hook_event_name":"PreToolUse","session_id":"e2e-log","tool_name":"Bash","tool_input":{"command":"make lint"}}`)
	if out, _ := tuiosCLI(t, base, "get-agent-state", "-s", "e2e-agent", "-w", "0", "--json"); !strings.Contains(out, `"now": "Bash: make lint"`) {
		t.Fatalf("the running tool is not in now:\n%s", out)
	}
	hook(`{"hook_event_name":"Notification","notification_type":"idle_prompt","session_id":"e2e-log"}`)
	out, err = tuiosCLI(t, base, "get-agent-state", "-s", "e2e-agent", "-w", "0", "--json")
	if err != nil || !strings.Contains(out, `"state": "idle"`) {
		t.Fatalf("get-agent-state after idle_prompt: %v\n%s", err, out)
	}
	if strings.Contains(out, `"now"`) {
		t.Errorf("an idle pane kept now:\n%s", out)
	}
}
