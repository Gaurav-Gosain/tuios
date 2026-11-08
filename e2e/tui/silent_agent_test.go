package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestASilentWorkerStillSelectsAsWorking: a worker reports working and then
// says nothing for longer than [agents] silent_after, which is what one long
// command does (a full test run in a Bash tool call). Its last word was
// working, and list-windows kept saying so, but list-agents showed it as
// unknown, and a lead watching its crew with --select state:working lost it.
//
// How this could pass wrongly, written down first:
//   - The pane could be checked before it goes silent. The test waits until
//     list-agents gives reason silent, and checks then.
//   - The fix could change what the JSON state field holds, which scripts
//     read. It must stay unknown, with last_state and reason beside it.
//   - state:working could start to match every pane. A pane that reported
//     idle must not match it, silent or not.
//   - The silence could stick after the agent speaks again. A new report must
//     clear the reason, and the table go back to plain working.
//
// The outputs are saved under artifactDir.
//
// Negative control: on origin/main --select state:working returns no row for
// the silent pane, and the first assertion fails.
func TestASilentWorkerStillSelectsAsWorking(t *testing.T) {
	const sess = "e2e-silent"
	base := t.TempDir()
	writeConfig(t, base, "[agents]\nsilent_after = \"3s\"\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	for _, name := range []string{"builder", "resting"} {
		if out, err := tuiosCLI(t, base, "new-window", "-s", sess, name, "--no-focus"); err != nil {
			t.Fatalf("new-window %s: %v\n%s", name, err, out)
		}
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", sess, "-w", "builder", "--harness", "claude-code", "-m", "Running the e2e suite"); err != nil {
		t.Fatalf("report working: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "idle", "-s", sess, "-w", "resting", "--harness", "claude-code"); err != nil {
		t.Fatalf("report idle: %v\n%s", err, out)
	}

	type row struct {
		Name      string `json:"name"`
		State     string `json:"state"`
		LastState string `json:"last_state"`
		Reason    string `json:"reason"`
	}
	rowsOf := func(args ...string) []row {
		t.Helper()
		out, err := tuiosCLI(t, base, append([]string{"list-agents", "--json"}, args...)...)
		if err != nil {
			t.Fatalf("list-agents %v: %v\n%s", args, err, out)
		}
		var res struct {
			Agents []row `json:"agents"`
		}
		if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &res); err != nil {
			t.Fatalf("list-agents --json: %v\n%s", err, out)
		}
		return res.Agents
	}
	names := func(rows []row) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.Name)
		}
		return out
	}

	// Wait for the silence.
	var builder row
	deadline := time.Now().Add(30 * time.Second)
	for {
		for _, r := range rowsOf("-s", sess) {
			if r.Name == "builder" {
				builder = r
			}
		}
		if builder.Reason == "silent" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the builder never went silent: %+v", builder)
		}
		time.Sleep(250 * time.Millisecond)
	}

	sel := rowsOf("--select", "state:working", "-s", sess)
	saveSyncArtifact(t, "select-working.txt", strings.Join(names(sel), "\n"))
	if got := names(sel); len(got) != 1 || got[0] != "builder" {
		t.Fatalf("ASSERTION: --select state:working gives %v for a silent worker and an idle one, want [builder]", got)
	}
	if builder.State != "unknown" || builder.LastState != "working" {
		t.Fatalf("ASSERTION: the silent worker's JSON is state %q last_state %q, want unknown and working", builder.State, builder.LastState)
	}
	if got := names(rowsOf("--select", "state:silent", "-s", sess)); len(got) != 1 || got[0] != "builder" {
		t.Fatalf("ASSERTION: --select state:silent gives %v, want [builder]", got)
	}
	table, err := tuiosCLI(t, base, "list-agents", "-s", sess)
	if err != nil {
		t.Fatalf("list-agents: %v\n%s", err, table)
	}
	saveSyncArtifact(t, "list-agents-silent.txt", table)
	if !strings.Contains(table, "working (silent ") {
		t.Fatalf("ASSERTION: the table does not say the worker works and is silent:\n%s", table)
	}

	lw, err := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, lw)
	}
	var wl struct {
		Windows []struct {
			Name   string `json:"custom_name"`
			State  string `json:"agent_state"`
			Reason string `json:"agent_reason"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(lw[strings.Index(lw, "{"):]), &wl); err != nil {
		t.Fatalf("list-windows --json: %v\n%s", err, lw)
	}
	for _, w := range wl.Windows {
		if w.Name == "builder" && (w.State != "working" || w.Reason != "silent") {
			t.Fatalf("ASSERTION: list-windows gives the builder %q with reason %q, want working and silent", w.State, w.Reason)
		}
	}

	// The worker speaks again: the silence goes.
	if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", sess, "-w", "builder", "--harness", "claude-code", "-m", "Reading the failures"); err != nil {
		t.Fatalf("report working again: %v\n%s", err, out)
	}
	for _, r := range rowsOf("-s", sess) {
		if r.Name == "builder" && (r.State != "working" || r.Reason != "") {
			t.Fatalf("ASSERTION: after a new report the builder is %+v, want working with no reason", r)
		}
	}
	table, _ = tuiosCLI(t, base, "list-agents", "-s", sess)
	saveSyncArtifact(t, "list-agents-spoke-again.txt", table)
	if strings.Contains(table, "silent") {
		t.Fatalf("ASSERTION: the table still says silent after a new report:\n%s", table)
	}
}
