package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// piHook runs the real Pi hook for a pane in dir, with --explain, the way the
// managed pi extension (v4) sends its reports: the event name and its payload
// on stdin. It is an error when the hook fails or prints anything on stdout,
// which nothing reads. Like subagentHook, it touches nothing of the test's,
// so several may run at once.
func piSubagentHook(base, dir, session, payload string) (string, error) {
	cmd := exec.Command(tuiosBin, "agent-hook", "pi", "--integration", "4", "--session", session, "--window", "0", "--timeout", "10s", "--explain")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Stdin = strings.NewReader(payload)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	explained := fmt.Sprintf("hook %-9s %s", session, strings.TrimSpace(stderr.String()))
	if err != nil || stdout.Len() != 0 {
		return explained, fmt.Errorf("the pi hook for %s failed or printed %q: %v", payload, stdout.String(), err)
	}
	return explained, nil
}

// piSubagentRow is one entry of get-agent-state's subagent_list.
type piSubagentRow struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Description string `json:"description"`
	State       string `json:"state"`
	Now         string `json:"now"`
	Last        string `json:"last"`
	Result      string `json:"result"`
}

// piSubagentRows reads a pane's subagent_list from get-agent-state --json.
func piSubagentRows(t *testing.T, base, session string) ([]piSubagentRow, int) {
	t.Helper()
	out, err := tuiosCLI(t, base, "get-agent-state", "--json", "-s", session, "-w", "0")
	if err != nil {
		t.Fatalf("get-agent-state %s: %v\n%s", session, err, out)
	}
	var st struct {
		Subagents int             `json:"subagents"`
		List      []piSubagentRow `json:"subagent_list"`
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("get-agent-state %s json: %v\n%s", session, err, out)
	}
	return st.List, st.Subagents
}

// waitPiSubagent waits until want says true about the pane's subagent_list,
// then returns the last read.
func waitPiSubagent(t *testing.T, base, session, what string, want func(list []piSubagentRow, count int) bool) []piSubagentRow {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		list, count := piSubagentRows(t, base, session)
		if want(list, count) {
			return list
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane's subagents never reached %s: list %+v count %d", what, list, count)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// TestPiSubagentRowsThroughTheRealHook drives the managed pi extension's v4
// subagent events through the real `tuios agent-hook pi`, against a real
// daemon and client, the way the extension forwards pi-subagents' runs:
//
//   - two subagents started under a working pane put "2 subagents" in the
//     pane's metadata and get-agent-state, and the rows name their type and
//     what they were asked;
//   - an update with a tool puts "bash: go test ./..." in the row's now;
//   - a stop with a result text ends its row done, a stop with none ends it
//     stopped, and neither moves the pane's own state;
//   - a stop for an id the pane never started changes nothing;
//   - a payload whose agent_id is not an id tuios keeps is skipped, the hook
//     still exiting 0 with empty stdout;
//   - after a daemon restart the set starts empty, and a lone stop for an
//     old id changes nothing;
//   - the events reach list-agents and agent-log like any harness's.
//
// It leaves a log of every hook's decision and the pane's state at each step,
// and frames of the client, under TUIOS_E2E_FRAMES.
//
// Negative control: against a build of the base commit, whose pi mapping
// skips these events ("event not mapped"), the wait for "2 subagents" in the
// metadata fails.
func TestPiSubagentRowsThroughTheRealHook(t *testing.T) {
	log := &stateLog{name: "pi-subagents-rows"}
	defer log.save(t)
	base := t.TempDir()
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\n")
	killDaemon(t, base)
	for _, name := range []string{"e2e-home", "e2e-pi"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
	}
	dir := workDirIn(t, base)
	hook := func(payload string) {
		t.Helper()
		explained, err := piSubagentHook(base, dir, "e2e-pi", payload)
		log.add("%s", explained)
		if err != nil {
			t.Fatal(err)
		}
	}
	step := func(name string, list []piSubagentRow, count int) {
		t.Helper()
		st := readAgentState(t, base, "e2e-pi", "0")
		log.add("%-44s state=%s subagents=%d rows=%+v", name, st.State, count, list)
	}

	// The pane's agent is at work when the runs start, the way it is while a
	// foreground child holds its turn.
	hook(`{"hook_event_name":"session_start","session_id":"pi-1"}`)
	hook(`{"hook_event_name":"agent_start","session_id":"pi-1"}`)
	if st := readAgentState(t, base, "e2e-pi", "0"); st.State != "working" {
		t.Fatalf("pane state = %q, want working after agent_start", st.State)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-home"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}

	// Two subagents start at once.
	hook(`{"hook_event_name":"subagent_start","session_id":"pi-1","agent_id":"9f0c2e71d4b5a683","agent_type":"researcher","text":"Review auth boundaries"}`)
	hook(`{"hook_event_name":"subagent_start","session_id":"pi-1","agent_id":"run-docs-sweep-0c2e71d4","agent_type":"worker","text":"Sweep the docs"}`)
	waitMeta(t, base, map[string]string{"subagents": "2 subagents"}, "-s", "e2e-pi", "-w", "0")
	waitText(t, term, "the runs on the rail", "2 of 2", "Review auth", "Sweep the docs")
	saveFrame(t, term, "pi-subagents-two-started")
	list, count := piSubagentRows(t, base, "e2e-pi")
	step("two subagents started", list, count)
	if count != 2 {
		t.Fatalf("get-agent-state says %d subagents, want 2", count)
	}
	for id, typ := range map[string]string{"9f0c2e71d4b5a683": "researcher", "run-docs-sweep-0c2e71d4": "worker"} {
		found := false
		for _, row := range list {
			if row.ID == id && row.Type == typ {
				found = true
			}
		}
		if !found {
			t.Fatalf("the rows do not name subagent %s (%s): %+v", id, typ, list)
		}
	}
	if list[0].Description == "" {
		t.Fatalf("the rows carry no description: %+v", list)
	}

	// What a subagent runs now shows on its row.
	hook(`{"hook_event_name":"subagent_update","session_id":"pi-1","agent_id":"9f0c2e71d4b5a683","tool":"bash","target":"go test ./..."}`)
	list = waitPiSubagent(t, base, "e2e-pi", "the tool line on the row", func(list []piSubagentRow, count int) bool {
		for _, row := range list {
			if row.ID == "9f0c2e71d4b5a683" && strings.Contains(row.Now, "go test ./...") {
				return true
			}
		}
		return false
	})
	log.add("update with a tool: rows=%+v", list)
	if n := func() int { _, c := piSubagentRows(t, base, "e2e-pi"); return c }(); n != 2 {
		t.Fatalf("a tool update moved the count to %d, want 2", n)
	}

	// One subagent finishes with a result, one is stopped with none. The
	// pane's own state is not theirs to move.
	hook(`{"hook_event_name":"subagent_stop","session_id":"pi-1","agent_id":"9f0c2e71d4b5a683","text":"Wrote the brief"}`)
	list = waitPiSubagent(t, base, "e2e-pi", "the first subagent done", func(list []piSubagentRow, count int) bool {
		for _, row := range list {
			if row.ID == "9f0c2e71d4b5a683" && row.State == "done" && row.Result == "Wrote the brief" {
				return true
			}
		}
		return false
	})
	if count := func() int { _, c := piSubagentRows(t, base, "e2e-pi"); return c }(); count != 1 {
		t.Fatalf("get-agent-state says %d subagents after one finished, want 1", count)
	}
	step("one subagent done", list, 1)
	hook(`{"hook_event_name":"subagent_stop","session_id":"pi-1","agent_id":"run-docs-sweep-0c2e71d4"}`)
	list = waitPiSubagent(t, base, "e2e-pi", "the second subagent stopped", func(list []piSubagentRow, count int) bool {
		for _, row := range list {
			if row.ID == "run-docs-sweep-0c2e71d4" && row.State == "stopped" {
				return true
			}
		}
		return false
	})
	if count := func() int { _, c := piSubagentRows(t, base, "e2e-pi"); return c }(); count != 0 {
		t.Fatalf("get-agent-state says %d subagents after both ended, want 0", count)
	}
	if st := readAgentState(t, base, "e2e-pi", "0"); st.State != "working" {
		t.Fatalf("the subagents moved the pane's state to %q, want still working", st.State)
	}
	saveFrame(t, term, "pi-subagents-both-ended")
	step("both subagents ended", list, 0)

	// A stop for an id the pane never started is nothing.
	hook(`{"hook_event_name":"subagent_stop","session_id":"pi-1","agent_id":"a0000000000000000","text":"nothing"}`)
	if _, count := piSubagentRows(t, base, "e2e-pi"); count != 0 {
		t.Fatalf("a stop for an unknown subagent moved the count to %d", count)
	}
	step("stop for an unknown id", list, 0)

	// An id tuios does not keep is skipped, and the hook still exits 0 with
	// empty stdout, which is what the extension's fire-and-forget spawn sees.
	hook(`{"hook_event_name":"subagent_start","session_id":"pi-1","agent_id":"bad id!","agent_type":"researcher"}`)
	if _, count := piSubagentRows(t, base, "e2e-pi"); count != 0 {
		t.Fatalf("an invalid agent_id was counted: count %d", count)
	}
	step("invalid agent_id skipped", list, 0)

	// The events reached list-agents and agent-log like any harness's.
	raw, err := tuiosCLI(t, base, "list-agents", "--all-sessions", "--json")
	if err != nil {
		t.Fatalf("list-agents: %v\n%s", err, raw)
	}
	var listing struct {
		Agents []struct {
			Session   string          `json:"session"`
			Subagents int             `json:"subagents"`
			List      []piSubagentRow `json:"subagent_list"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(raw), &listing); err != nil {
		t.Fatalf("list-agents json: %v\n%s", err, raw)
	}
	for _, a := range listing.Agents {
		if a.Session != "e2e-pi" {
			continue
		}
		if a.Subagents != 0 || len(a.List) != 2 {
			t.Fatalf("list-agents e2e-pi: subagents %d with %d rows, want 0 running with both finished rows", a.Subagents, len(a.List))
		}
	}
	out, err := tuiosCLI(t, base, "agent-log", "-s", "e2e-pi", "-w", "0")
	if err != nil ||
		!strings.Contains(out, "subagent  Review auth boundaries (researcher) started") ||
		!strings.Contains(out, "subagent  Review auth boundaries (researcher) done") ||
		!strings.Contains(out, "subagent  Sweep the docs (worker) stopped") {
		t.Fatalf("agent-log does not hold the subagents' starts, the finish and the stop: %v\n%s", err, out)
	}
	log.add("agent-log e2e-pi:\n%s", out)

	// A daemon restart starts the set empty, and a lone stop for an old id
	// changes nothing. The pane comes back rehydrated; the subagent set does
	// not (it is daemon memory only).
	killDaemon(t, base)
	hook(`{"hook_event_name":"session_start","session_id":"pi-1"}`)
	hook(`{"hook_event_name":"subagent_stop","session_id":"pi-1","agent_id":"9f0c2e71d4b5a683","text":"Wrote the brief"}`)
	if _, count := piSubagentRows(t, base, "e2e-pi"); count != 0 {
		t.Fatalf("a stop after a daemon restart was counted: count %d, want 0", count)
	}
	step("after the daemon restart", list, 0)
	alive(t, term, "after the restart")
}
