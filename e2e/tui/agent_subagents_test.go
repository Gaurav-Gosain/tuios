package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// subagentHook runs the real Claude Code hook for a pane in dir, with
// --explain, and returns the line it explained its decision with. It is an
// error when the hook fails or prints anything on stdout, which Claude Code
// would read as an answer. It touches nothing of the test's, so several may
// run at once, as Claude Code runs the hooks of subagents started together.
func subagentHook(base, dir, session, payload string) (string, error) {
	cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", session, "--window", "0", "--timeout", "10s", "--explain")
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
		return explained, fmt.Errorf("the hook for %s failed or printed %q: %v", payload, stdout.String(), err)
	}
	return explained, nil
}

// subagentCountOf reads get-agent-state's subagents figure for a pane.
func subagentCountOf(t *testing.T, base, session string) int {
	t.Helper()
	out, err := tuiosCLI(t, base, "get-agent-state", "--json", "-s", session, "-w", "0")
	if err != nil {
		t.Fatalf("get-agent-state %s: %v\n%s", session, err, out)
	}
	var st struct {
		Subagents int `json:"subagents"`
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("get-agent-state %s json: %v\n%s", session, err, out)
	}
	return st.Subagents
}

// TestSubagentCountOnARestingRow drives Claude Code's SubagentStart and
// SubagentStop through the real `tuios agent-hook`, against a real daemon and
// client, on a pane whose main agent handed work to subagents and finished
// its turn, which is when the pane used to look as if nothing were running:
//
//   - three subagents started at once (Claude Code runs their hooks in
//     parallel) put "3 subagents" in the pane's metadata and on its rail row,
//     and get-agent-state says 3, while the pane stays done with its message;
//   - with the context warning in front of it, the harness gives way so the
//     count stays on the row;
//   - a stop for an id the pane never started changes nothing, not even the
//     activity ring, and neither does a start from another conversation;
//   - a stop takes the count to "2 subagents";
//   - a row at rest with a subagent at work is not folded into "+N at rest";
//   - list-agents reports the figure, and agent-log the session start, the
//     three starts and the one stop it knew, done since it said something;
//   - a SessionStart (resume) forgets them: the metadata and the row lose the
//     count, and a SessionEnd does the same for the other pane.
//
// It leaves a log of every hook's decision and the pane's state at each step,
// and frames of the rail, under TUIOS_E2E_FRAMES.
//
// Negative control: against a build of the base commit, which reports nothing
// for SubagentStart, the wait for "3 subagents" in the metadata fails. With
// the sidebarNoteKeepSubagents call taken out of sidebarAgentNoteRow, the row
// reads "claude · ctx 91%" and the wait for the count beside the warning
// fails. With the session clause taken out of activityReportGuard, the other
// conversation's subagent is counted and the check after it fails. With the
// subagents check taken out of sidebarAgentRests, the row with a subagent
// stays folded and the wait for "+2 at rest" with "1 subagent" fails.
func TestSubagentCountOnARestingRow(t *testing.T) {
	log := &stateLog{name: "subagents-resting-row"}
	defer log.save(t)
	base := t.TempDir()
	// The count alone on the row, which is what this test holds; the rows
	// under it are TestSubagentRowsOnTheRail's.
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\nagent_rest_fold = \"1s\"\nsubagent_rows = \"count\"\n")
	killDaemon(t, base)
	for _, name := range []string{"e2e-home", "e2e-lead", "e2e-ra", "e2e-rb", "e2e-rc"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
	}
	dir := workDirIn(t, base)
	hook := func(session, payload string) {
		t.Helper()
		explained, err := subagentHook(base, dir, session, payload)
		log.add("%s", explained)
		if err != nil {
			t.Fatal(err)
		}
	}
	step := func(name, session string) agentStateJSON {
		t.Helper()
		st := readAgentState(t, base, session, "0")
		log.add("%-44s state=%s message=%q meta=%v subagents=%d", name, st.State, st.Message, agentMeta(t, base, "-s", session, "-w", "0"), subagentCountOf(t, base, session))
		return st
	}

	// The lead finishes a turn that handed the review out; three other
	// agents sit at rest long enough to fold.
	hook("e2e-lead", `{"hook_event_name":"SessionStart","session_id":"lead-1","source":"startup"}`)
	hook("e2e-lead", `{"hook_event_name":"UserPromptSubmit","session_id":"lead-1","prompt":"review the api in parallel"}`)
	hook("e2e-lead", `{"hook_event_name":"Stop","session_id":"lead-1","last_assistant_message":"Handed the review to three agents."}`)
	for _, name := range []string{"e2e-ra", "e2e-rb", "e2e-rc"} {
		hook(name, `{"hook_event_name":"SessionStart","session_id":"`+name+`","source":"startup"}`)
	}
	time.Sleep(1500 * time.Millisecond)

	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-home"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	waitText(t, term, "the finished lead and the folded rows", "Handed the", "+3 at rest")
	step("lead done, three at rest", "e2e-lead")

	// Three subagents start at once.
	starts := []string{
		`{"hook_event_name":"SubagentStart","session_id":"lead-1","agent_id":"a3f09c2e71d4b5a68","agent_type":"Explore"}`,
		`{"hook_event_name":"SubagentStart","session_id":"lead-1","agent_id":"a71d4b5a683f09c2e","agent_type":"general-purpose"}`,
		`{"hook_event_name":"SubagentStart","session_id":"lead-1","agent_id":"aworker-e-quickfix-0c2e71d4b5a683f9","agent_type":"worker-e-quickfix"}`,
	}
	var wg sync.WaitGroup
	explained := make([]string, len(starts))
	errs := make([]error, len(starts))
	for i, payload := range starts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			explained[i], errs[i] = subagentHook(base, dir, "e2e-lead", payload)
		}()
	}
	wg.Wait()
	for i := range starts {
		log.add("%s", explained[i])
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
	}
	waitMeta(t, base, map[string]string{"subagents": "3 subagents"}, "-s", "e2e-lead", "-w", "0")
	waitText(t, term, "the count on the finished row", "3 subagents")
	saveFrame(t, term, "subagents-three-on-done-row")
	st := step("three subagents started", "e2e-lead")
	if st.State != "done" || st.Message != "Handed the review to three agents." {
		t.Fatalf("the subagents moved the lead's state: %+v, want done with its message", st)
	}
	if n := subagentCountOf(t, base, "e2e-lead"); n != 3 {
		t.Fatalf("get-agent-state says %d subagents, want 3", n)
	}

	// With the context warning in front of it, the line has no room for the
	// harness as well, and the harness gives way to the count.
	if out, err := tuiosCLI(t, base, "set-agent-meta", "-s", "e2e-lead", "-w", "0", "--source", "statusline", "context=91%"); err != nil {
		t.Fatalf("set-agent-meta: %v\n%s", err, out)
	}
	waitText(t, term, "the count beside the context warning", "ctx 91% · 3 subagents")
	saveFrame(t, term, "subagents-with-context-warning")

	// A stop for an id the pane never started is nothing. The hook has
	// returned once the daemon answered, so one read is enough.
	hook("e2e-lead", `{"hook_event_name":"SubagentStop","session_id":"lead-1","agent_id":"a0000000000000000","agent_type":"Explore"}`)
	if m := agentMeta(t, base, "-s", "e2e-lead", "-w", "0"); m["subagents"] != "3 subagents" {
		t.Fatalf("a stop for an unknown subagent moved the count: %v", m)
	}
	step("stop for an unknown id", "e2e-lead")

	// A subagent of another conversation, such as one a `claude -p` the lead
	// left running in the background starts, is not the pane's, although the
	// pane is at rest and a state report from that conversation would take it
	// over.
	hook("e2e-lead", `{"hook_event_name":"SubagentStart","session_id":"nested-1","agent_id":"a5a683f09c2e71d4b","agent_type":"Explore"}`)
	if m := agentMeta(t, base, "-s", "e2e-lead", "-w", "0"); m["subagents"] != "3 subagents" {
		t.Fatalf("another conversation's subagent was counted on the pane: %v", m)
	}
	step("another conversation's subagent", "e2e-lead")

	hook("e2e-lead", `{"hook_event_name":"SubagentStop","session_id":"lead-1","agent_id":"a3f09c2e71d4b5a68","agent_type":"Explore","last_assistant_message":"Mapped the api."}`)
	waitMeta(t, base, map[string]string{"subagents": "2 subagents"}, "-s", "e2e-lead", "-w", "0")
	waitText(t, term, "the count after a stop", "2 subagents")
	if st := step("one subagent stopped", "e2e-lead"); st.State != "done" || st.Message != "Handed the review to three agents." {
		t.Fatalf("a subagent stopping moved the lead's state: %+v, want done with its message", st)
	}

	// A row at rest with a subagent at work comes out of the fold.
	hook("e2e-ra", `{"hook_event_name":"SubagentStart","session_id":"e2e-ra","agent_id":"ab1c2d3e4f5a6b7c8","agent_type":"Explore"}`)
	waitMeta(t, base, map[string]string{"subagents": "1 subagent"}, "-s", "e2e-ra", "-w", "0")
	waitText(t, term, "the resting row with a subagent out of the fold", "1 subagent", "+2 at rest")
	saveFrame(t, term, "subagents-resting-row-unfolded")
	step("a resting pane starts a subagent", "e2e-ra")

	raw, err := tuiosCLI(t, base, "list-agents", "--all-sessions", "--json")
	if err != nil {
		t.Fatalf("list-agents: %v\n%s", err, raw)
	}
	var listing struct {
		Agents []struct {
			Session   string            `json:"session"`
			Subagents int               `json:"subagents"`
			Meta      map[string]string `json:"meta"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(raw), &listing); err != nil {
		t.Fatalf("list-agents json: %v\n%s", err, raw)
	}
	counts := map[string]int{}
	for _, a := range listing.Agents {
		counts[a.Session] = a.Subagents
	}
	if counts["e2e-lead"] != 2 || counts["e2e-ra"] != 1 || counts["e2e-rb"] != 0 {
		t.Fatalf("list-agents subagents = %v, want lead 2, ra 1, rb 0\n%s", counts, raw)
	}
	out, err := tuiosCLI(t, base, "agent-log", "-s", "e2e-lead", "-w", "0")
	if err != nil || !strings.Contains(out, "subagent  Explore started") || strings.Count(out, "subagent  Explore done") != 1 ||
		!strings.Contains(out, "session   started (startup)") {
		t.Fatalf("agent-log does not hold the session start, the subagents' starts and the one stop it knew: %v\n%s", err, out)
	}
	log.add("agent-log e2e-lead:\n%s", out)

	// A resumed session starts with none, and an ended one keeps none.
	hook("e2e-lead", `{"hook_event_name":"SessionStart","session_id":"lead-1","source":"resume"}`)
	waitMeta(t, base, map[string]string{"subagents": ""}, "-s", "e2e-lead", "-w", "0")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "2 subagents") }, uiTimeout); err != nil {
		t.Fatalf("the lead's row kept its count after the session resumed: %v\n%s", err, term.Snapshot())
	}
	if n := subagentCountOf(t, base, "e2e-lead"); n != 0 {
		t.Fatalf("get-agent-state says %d subagents after a resume, want 0", n)
	}
	step("the session resumed", "e2e-lead")
	hook("e2e-ra", `{"hook_event_name":"SessionEnd","session_id":"e2e-ra","reason":"prompt_input_exit"}`)
	if n := subagentCountOf(t, base, "e2e-ra"); n != 0 {
		t.Fatalf("get-agent-state says %d subagents after the session ended, want 0", n)
	}
	step("the other session ended", "e2e-ra")
	saveFrame(t, term, "subagents-cleared")
	alive(t, term, "after the subagents came and went")
}
