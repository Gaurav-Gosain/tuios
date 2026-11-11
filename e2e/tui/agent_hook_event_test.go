package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// agent-hook-event runs a harness hook event in the daemon, through the real
// socket, from a process inside a pane, as an installed hook would send it.
//
// How these could pass wrongly, written down first:
//   - The report could land on a pane the call named instead of the one it
//     came from. The verb takes no pane param at all, a second pane runs the
//     same event, and each pane must hold only its own conversation.
//   - The env param could carry the pane. A call naming TUIOS_PANE_ID in env
//     must be refused, and a call from outside every pane must report
//     nothing on any pane.
//   - stdout could be empty because the event printed nothing anyway. A
//     Claude Code SessionStart from inside a pane prints the lines that tell
//     the agent where it runs, so the stdout must hold them.
//   - The state could come from something else than the verb. The panes are
//     plain shells with no harness, and each state is read back after its
//     call, with the conversation id the payload named.
//
// The artifact is the call results and the agent states, written to
// agent-hook-event.txt in the test's output folder.

// hookEventScript runs one agent-hook-event call from the pane it is typed
// into and writes the result to a file, renamed into place when complete.
const hookEventScript = `#!/bin/sh
"$TUIOS_E2E_CLI" call agent-hook-event "$(cat "$1")" > "$2.tmp" 2>&1
echo "exit=$?" >> "$2.tmp"
mv "$2.tmp" "$2"
`

func TestAgentHookEventReportsForTheCallingPane(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-hookev"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "OTHER", "--no-focus"); err != nil {
		t.Fatalf("open the second pane: %v\n%s", err, out)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(script, []byte(hookEventScript), 0o700); err != nil {
		t.Fatal(err)
	}
	var artifact strings.Builder

	// fromPane types the call into a pane's shell and returns what it wrote.
	n := 0
	fromPane := func(window string, params map[string]any) string {
		t.Helper()
		n++
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		in := filepath.Join(dir, "params-"+itoa(n)+".json")
		out := filepath.Join(dir, "result-"+itoa(n)+".txt")
		if err := os.WriteFile(in, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		line := "TUIOS_E2E_CLI=" + tuiosBin + " sh " + script + " " + in + " " + out + "\n"
		if o, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", window, line); err != nil {
			t.Fatalf("type the call into %s: %v\n%s", window, err, o)
		}
		deadline := time.Now().Add(uiTimeout)
		for {
			if data, err := os.ReadFile(out); err == nil {
				fmt.Fprintf(&artifact, "call %d from %s:\n%s\n", n, window, data)
				return string(data)
			}
			if time.Now().After(deadline) {
				t.Fatalf("the call from %s never finished", window)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	state := func(window string) map[string]any {
		t.Helper()
		out, err := tuiosCLI(t, base, "get-agent-state", "--json", "-s", sess, "-w", window)
		if err != nil {
			t.Fatalf("get-agent-state %s: %v\n%s", window, err, out)
		}
		fmt.Fprintf(&artifact, "state of %s: %s\n", window, out)
		var st map[string]any
		if err := json.Unmarshal([]byte(out), &st); err != nil {
			t.Fatalf("get-agent-state %s: %v\n%s", window, err, out)
		}
		return st
	}
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(artifactDir(t), "agent-hook-event.txt"), []byte(artifact.String()), 0o600)
	})

	// A session start from pane 0 reports idle for pane 0 and prints where
	// the agent runs.
	res := fromPane("0", map[string]any{
		"harness": "claude-code",
		"payload": `{"hook_event_name":"SessionStart","session_id":"conv-zero","source":"startup"}`,
	})
	if !strings.Contains(res, "exit=0") || !strings.Contains(res, "tuios --skill") {
		t.Fatalf("the session start from pane 0 did not print the agent's context:\n%s", res)
	}
	if st := state("0"); st["state"] != "idle" || st["agent_session_id"] != "conv-zero" {
		t.Fatalf("pane 0 after its session start: %v", st)
	}

	// A prompt from the other pane moves only that pane.
	fromPane("OTHER", map[string]any{
		"harness": "claude",
		"payload": `{"hook_event_name":"UserPromptSubmit","session_id":"conv-other","prompt":"run the tests"}`,
	})
	if st := state("OTHER"); st["state"] != "working" || st["agent_session_id"] != "conv-other" {
		t.Fatalf("the other pane after its prompt: %v", st)
	}
	if st := state("0"); st["state"] != "idle" || st["agent_session_id"] != "conv-zero" {
		t.Fatalf("pane 0 changed when the other pane reported: %v", st)
	}

	// The env param cannot name a pane.
	res = fromPane("OTHER", map[string]any{
		"harness": "claude-code",
		"payload": `{"hook_event_name":"Stop","session_id":"conv-zero"}`,
		"env":     map[string]string{"TUIOS_PANE_ID": "0"},
	})
	if strings.Contains(res, "exit=0") || !strings.Contains(res, "invalid_params") {
		t.Fatalf("a call naming TUIOS_PANE_ID in env was not refused:\n%s", res)
	}

	// From outside every pane the event reports for no pane.
	out, err := tuiosCLI(t, base, "call", "agent-hook-event",
		`{"harness":"claude-code","payload":"{\"hook_event_name\":\"Stop\",\"session_id\":\"conv-zero\"}","explain":true}`)
	fmt.Fprintf(&artifact, "call from outside every pane:\n%s\n", out)
	if err != nil {
		t.Fatalf("the call from outside every pane failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no pane") {
		t.Fatalf("the call from outside every pane found a pane:\n%s", out)
	}
	if st := state("0"); st["state"] != "idle" {
		t.Fatalf("a call from outside every pane moved pane 0: %v", st)
	}
}
