package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// fakeClaudeScript stands in for Claude Code in a pane: it replays a scenario
// of hook payloads through the real `tuios agent-hook claude-code`, the way
// Claude Code runs its hooks, one process per event with the payload on
// stdin. A line is one of:
//
//	{...}          a payload, run and waited for
//	& {...}        a payload run at once with the ones after it, as Claude
//	               Code runs the hooks of parallel tool calls
//	join           wait for the payloads started with &
//	wait LABEL     print "FAKE-AT LABEL" and wait for a line on the terminal
//
// Every hook runs with --explain, and what it decided goes to the log, which
// the test keeps as its artifact.
const fakeClaudeScript = `#!/bin/sh
scenario=$1
log=$2
hook() { printf '%s' "$1" | "__TUIOS__" agent-hook claude-code --timeout 10s --explain 2>>"$log"; }
while IFS= read -r line <&3; do
	case "$line" in
	''|'#'*) ;;
	'wait '*) wait; printf 'FAKE-AT %s\n' "${line#wait }"; read -r _ ;;
	'join') wait ;;
	'& '*) hook "${line#& }" & ;;
	*) hook "$line" ;;
	esac
done 3<"$scenario"
wait
printf 'FAKE-DONE\n'
`

// The conversation the scenario reports, and its first prompt, taken from a
// capture of Claude Code 2.1.296.
const (
	ccSession = "522be9ad-cf5e-4caa-8823-41b6963fc352"
	ccPrompt  = "823af7ab-616d-442b-890d-dc573d00e4ee"
)

// cc is a Claude Code hook payload of the main conversation: the common
// fields every captured payload carried, then the event's own. agent is the
// subagent the event fires in, "" for the main thread.
func cc(agent, fields string) string {
	return ccIn(ccSession, agent, fields)
}

// ccIn is cc for conversation session.
func ccIn(session, agent, fields string) string {
	common := `"session_id":"` + session + `","transcript_path":"/home/u/.claude/projects/-src/` + session + `.jsonl","cwd":"/src","prompt_id":"` + ccPrompt + `","permission_mode":"bypassPermissions"`
	if agent != "" {
		common += `,"agent_id":"` + agent + `","agent_type":"general-purpose"`
	}
	return "{" + common + `,"effort":{"level":"medium"},` + fields + "}"
}

// The subagents of the scenario, by the ids Claude Code gives them, and the
// tool calls that launched them.
const (
	saTmux  = "ab34da07d2d63ee3c"
	saRail  = "aa84fb4f695c1ee02"
	saDocs  = "a51c0e9d2b7f4a861"
	saCI    = "a7e3f1b90c2d48a56"
	callT   = "toolu_018oJpQiuuCwCqSvc1N1wGYd"
	callR   = "toolu_01SXXKhavWBrBWC2DB4ovmaD"
	callD   = "toolu_01Hq2wq8bJYkGv5xTn3Lm9Pa"
	callCI  = "toolu_01Ue6RkPq3dZbN8xWvC2fT4s"
	toolTmx = "toolu_01VUbFKSFoMYZATRvdAzAZLC"
)

// agentLaunch is the PreToolUse of the Agent tool, as captured.
func agentLaunch(call, desc, typ string, background bool) string {
	bg := "false"
	if background {
		bg = "true"
	}
	return cc("", `"hook_event_name":"PreToolUse","tool_name":"Agent","tool_input":{"description":"`+desc+`","prompt":"Do it, then reply done.","subagent_type":"`+typ+`","run_in_background":`+bg+`},"tool_use_id":"`+call+`"`)
}

// subagentStartOf is SubagentStart, which names the id and the type alone.
func subagentStartOf(id, typ string) string {
	return ccIn(ccSession, "", `"agent_id":"`+id+`","agent_type":"`+typ+`","hook_event_name":"SubagentStart"`)
}

// subagentStopOf is SubagentStop with what the subagent said last and the
// background tasks still running.
func subagentStopOf(id, typ, last, tasks string) string {
	return ccIn(ccSession, "", `"agent_id":"`+id+`","agent_type":"`+typ+`","hook_event_name":"SubagentStop","stop_hook_active":false,"agent_transcript_path":"/home/u/.claude/projects/-src/`+ccSession+`/subagents/agent-`+id+`.jsonl","last_assistant_message":"`+last+`","background_tasks":[`+tasks+`],"session_crons":[]`)
}

// task is one entry of background_tasks.
func task(id, desc, typ string) string {
	return `{"id":"` + id + `","type":"subagent","status":"running","description":"` + desc + `","agent_type":"` + typ + `"}`
}

// subagentTool is a tool call inside subagent id: PreToolUse, or PostToolUse
// with done.
func subagentTool(id, call, tool, input string, done bool) string {
	if done {
		return cc(id, `"hook_event_name":"PostToolUse","tool_name":"`+tool+`","tool_input":`+input+`,"tool_response":{"stdout":"ok","stderr":"","interrupted":false},"tool_use_id":"`+call+`","duration_ms":10`)
	}
	return cc(id, `"hook_event_name":"PreToolUse","tool_name":"`+tool+`","tool_input":`+input+`,"tool_use_id":"`+call+`"`)
}

// subagentScenario is what the fake claude replays: one background and three
// foreground launches, the two foreground ones of one type started in the
// other order (so the daemon's first guess at their descriptions is wrong
// until a Stop's background_tasks names them), a tool call in each, another
// conversation's tool call under a known id, then each way a subagent ends.
func subagentScenario() string {
	running := task(saTmux, "Research tmux", "general-purpose") + "," + task(saRail, "Audit the rail", "general-purpose") + "," + task(saDocs, "Write the docs", "general-purpose")
	lines := []string{
		"# launch",
		cc("", `"hook_event_name":"SessionStart","source":"startup"`),
		cc("", `"hook_event_name":"UserPromptSubmit","prompt":"Look at tmux, the rail, the docs and CI in parallel."`),
		agentLaunch(callT, "Research tmux", "general-purpose", true),
		subagentStartOf(saTmux, "general-purpose"),
		cc("", `"hook_event_name":"PostToolUse","tool_name":"Agent","tool_input":{"description":"Research tmux","prompt":"Do it, then reply done.","subagent_type":"general-purpose","run_in_background":true},"tool_response":{"isAsync":true,"status":"async_launched","agentId":"`+saTmux+`","description":"Research tmux","resolvedModel":"claude-haiku-5-5","prompt":"Do it, then reply done.","canReadOutputFile":true},"tool_use_id":"`+callT+`","duration_ms":3`),
		agentLaunch(callR, "Audit the rail", "general-purpose", false),
		agentLaunch(callD, "Write the docs", "general-purpose", false),
		subagentStartOf(saDocs, "general-purpose"),
		subagentStartOf(saRail, "general-purpose"),
		agentLaunch(callCI, "Check CI", "Explore", false),
		subagentStartOf(saCI, "Explore"),
		"& " + subagentTool(saTmux, toolTmx, "Bash", `{"command":"go test ./...","description":"Run the tests"}`, false),
		"& " + subagentTool(saRail, "toolu_01r1", "Read", `{"file_path":"internal/app/render_sidebar.go"}`, false),
		"& " + subagentTool(saDocs, "toolu_01d1", "Edit", `{"file_path":"AGENTS.md","old_string":"a","new_string":"b"}`, false),
		"& " + subagentTool(saCI, "toolu_01c1", "Bash", `{"command":"gh run list","description":"List the runs"}`, false),
		"join",
		"wait tools",
		// A conversation the pane is not running, a nested claude -p,
		// reports a tool under an id the pane knows.
		ccIn("nested-0001", saTmux, `"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"FOREIGN-TOOL"},"tool_use_id":"toolu_01zz"`),
		// The person moved the two foreground agents to the background, and
		// the main thread ended its turn.
		cc("", `"hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"Handed the work to four agents.","background_tasks":[`+running+`],"session_crons":[]`),
		"wait launched",
		"# finish",
		subagentTool(saTmux, toolTmx, "Bash", `{"command":"go test ./...","description":"Run the tests"}`, true),
		subagentStopOf(saTmux, "general-purpose", "tmux keeps every pane in one server.", task(saRail, "Audit the rail", "general-purpose")+","+task(saDocs, "Write the docs", "general-purpose")),
		cc("", `"hook_event_name":"UserPromptSubmit","prompt":"Stop the docs agent."`),
		cc("", `"hook_event_name":"PreToolUse","tool_name":"TaskStop","tool_input":{"task_id":"`+saDocs+`"},"tool_use_id":"toolu_01st"`),
		subagentStopOf(saDocs, "general-purpose", "", task(saRail, "Audit the rail", "general-purpose")),
		cc("", `"hook_event_name":"PostToolUseFailure","tool_name":"Agent","tool_input":{"description":"Check CI","prompt":"Do it, then reply done.","subagent_type":"Explore","run_in_background":false},"tool_use_id":"`+callCI+`","error":"Agent failed: the API is rate limited\nretry later"`),
		subagentStopOf(saCI, "Explore", "", ""),
		subagentTool(saRail, "toolu_01r1", "Read", `{"file_path":"internal/app/render_sidebar.go"}`, true),
		subagentTool(saRail, "toolu_01r2", "Bash", `{"command":"go vet ./...","description":"Vet"}`, false),
		subagentTool(saRail, "toolu_01r2", "Bash", `{"command":"go vet ./...","description":"Vet"}`, true),
		subagentStopOf(saRail, "general-purpose", "The rail is fine.", ""),
		cc("", `"hook_event_name":"PostToolUse","tool_name":"Agent","tool_input":{"description":"Audit the rail","prompt":"Do it, then reply done.","subagent_type":"general-purpose","run_in_background":false},"tool_response":{"status":"completed","agentId":"`+saRail+`","agentType":"general-purpose","content":[{"type":"text","text":"The rail is fine."}],"totalDurationMs":2532,"totalTokens":13450,"totalToolUseCount":2},"tool_use_id":"`+callR+`","duration_ms":2600`),
		cc("", `"hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"Three agents reported back.","background_tasks":[],"session_crons":[]`),
		"wait finished",
	}
	return strings.Join(lines, "\n") + "\n"
}

// subagentRow is one entry of get-agent-state's subagent_list.
type subagentRow struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Description string `json:"description"`
	State       string `json:"state"`
	StartedAt   int64  `json:"started_at"`
	EndedAt     int64  `json:"ended_at"`
	Now         string `json:"now"`
	Last        string `json:"last"`
	Tools       int    `json:"tools"`
	Result      string `json:"result"`
}

// subagentsOf is a pane's state, its count and its list, from
// get-agent-state, and the JSON it read them from.
type subagentsOf struct {
	State     string        `json:"state"`
	Message   string        `json:"message"`
	Subagents int           `json:"subagents"`
	List      []subagentRow `json:"subagent_list"`
	raw       string
}

func readSubagents(t *testing.T, base, session string) subagentsOf {
	t.Helper()
	out, err := tuiosCLI(t, base, "get-agent-state", "--json", "-s", session, "-w", "0")
	if err != nil {
		t.Fatalf("get-agent-state %s: %v\n%s", session, err, out)
	}
	var st subagentsOf
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("get-agent-state %s json: %v\n%s", session, err, out)
	}
	st.raw = out
	return st
}

// byID is the row of the list with id.
func (s subagentsOf) byID(id string) subagentRow {
	for _, r := range s.List {
		if r.ID == id {
			return r
		}
	}
	return subagentRow{}
}

// waitSubagents polls the pane until ok holds, logs the list it saw and
// keeps it as an artifact under step's name.
func waitSubagents(t *testing.T, base, session string, log *stateLog, step string, ok func(subagentsOf) bool) subagentsOf {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		st := readSubagents(t, base, session)
		if ok(st) {
			log.add("%-36s state=%s message=%q subagents=%d", step, st.State, st.Message, st.Subagents)
			for _, r := range st.List {
				log.add("    %-17s %-8s %-16q type=%s now=%q last=%q tools=%d result=%q", r.ID, r.State, r.Description, r.Type, r.Now, r.Last, r.Tools, r.Result)
			}
			if dir := os.Getenv("TUIOS_E2E_FRAMES"); dir != "" {
				name := "subagent-list-" + strings.ReplaceAll(step, " ", "-") + ".json"
				_ = os.WriteFile(filepath.Join(dir, name), []byte(st.raw), 0o644)
			}
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: the pane never got there; it says:\n%s", step, st.raw)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestSubagentListFromHooks runs a stand-in claude in a pane that replays
// Claude Code's hook payloads, as captured from 2.1.296, through the real
// `tuios agent-hook claude-code`, against a real daemon with a real client
// attached. It holds the data path from the hook to the window:
//
//   - each subagent gets the description its launch gave it, the two of one
//     type started in the other order included, once a Stop's
//     background_tasks names them;
//   - the tool each one runs is its now, the four started at once held
//     back and pushed together, while the pane, whose main agent
//     ended its turn, stays done with its message and counts four;
//   - another conversation's tool call under a known id is not shown;
//   - a client pushing its own state keeps the list;
//   - each end: done with what it said and its tool count, stopped after a
//     TaskStop, failed with the error of the failed Agent call;
//   - the finished ones fade, done and stopped first, the failed one later;
//   - agent-log names the subagents and says how each ended.
//
// With TUIOS_E2E_OLD_BIN, a client of that build attached beside the new one
// still draws the count.
//
// It leaves the state log, the hook's --explain lines, the list at each step
// as JSON, and frames of the rail under TUIOS_E2E_FRAMES.
//
// Negative controls are in NEGATIVE_CONTROLS.md, "Subagent list".
func TestSubagentListFromHooks(t *testing.T) {
	log := &stateLog{name: "subagent-list"}
	defer log.save(t)
	t.Setenv("TUIOS_SUBAGENT_FADE_SECONDS", "2")
	base := t.TempDir()
	// The count alone on the row, as an older client draws it beside this one.
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\nsubagent_rows = \"count\"\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-sub", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(bin, "claude")
	if err := os.WriteFile(claude, []byte(strings.ReplaceAll(fakeClaudeScript, "__TUIOS__", tuiosBin)), 0o755); err != nil {
		t.Fatal(err)
	}
	scenario := filepath.Join(base, "scenario.txt")
	if err := os.WriteFile(scenario, []byte(subagentScenario()), 0o644); err != nil {
		t.Fatal(err)
	}
	hookLog := filepath.Join(base, "hooks.log")
	defer func() {
		if dir := os.Getenv("TUIOS_E2E_FRAMES"); dir != "" {
			if b, err := os.ReadFile(hookLog); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "subagent-list-hooks.log"), b, 0o644)
			}
		}
	}()

	term := attachIn(t, base, "e2e-sub", startOpts{})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-sub", "-w", "0", claude+" "+scenario+" "+hookLog+"\n"); err != nil {
		t.Fatalf("start the fake claude: %v\n%s", err, out)
	}
	// The four tool calls come at once, within subagentPublishGap of the
	// starts, so the daemon holds them back and pushes them together. The
	// two subagents of one type still have each other's descriptions: no
	// Stop has named them yet.
	waitCapture(t, base, "e2e-sub", "0", "FAKE-AT tools")
	waitSubagents(t, base, "e2e-sub", log, "tools", func(s subagentsOf) bool {
		return s.Subagents == 4 && s.byID(saTmux).Now == "Bash: go test ./..." &&
			s.byID(saRail).Now == "Read: internal/app/render_sidebar.go" &&
			s.byID(saDocs).Now == "Edit: AGENTS.md" &&
			s.byID(saCI).Now == "Bash: gh run list"
	})
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-sub", "-w", "0", "\n"); err != nil {
		t.Fatalf("step the fake claude: %v\n%s", err, out)
	}
	waitCapture(t, base, "e2e-sub", "0", "FAKE-AT launched")

	st := waitSubagents(t, base, "e2e-sub", log, "launched", func(s subagentsOf) bool {
		return s.Subagents == 4 && len(s.List) == 4 &&
			s.byID(saTmux).Description == "Research tmux" &&
			s.byID(saRail).Description == "Audit the rail" &&
			s.byID(saDocs).Description == "Write the docs" &&
			s.byID(saCI).Description == "Check CI" &&
			s.byID(saTmux).Now == "Bash: go test ./..." &&
			s.byID(saRail).Now == "Read: internal/app/render_sidebar.go" &&
			s.byID(saDocs).Now == "Edit: AGENTS.md" &&
			s.byID(saCI).Now == "Bash: gh run list"
	})
	if st.State != "done" || st.Message != "Handed the work to four agents." {
		t.Fatalf("the subagents moved the pane's state: %s %q, want done with its message", st.State, st.Message)
	}
	for i, id := range []string{saTmux, saDocs, saRail, saCI} {
		if r := st.List[i]; r.ID != id || r.State != "running" || r.Tools != 1 || r.StartedAt == 0 || r.EndedAt != 0 {
			t.Fatalf("row %d is %+v, want %s running, in start order, with one tool", i, r, id)
		}
	}
	if st.byID(saCI).Type != "Explore" || st.byID(saTmux).Type != "general-purpose" {
		t.Fatalf("the types are wrong:\n%s", st.raw)
	}
	if strings.Contains(st.raw, "FOREIGN-TOOL") {
		t.Fatalf("another conversation's tool call is on the list:\n%s", st.raw)
	}
	waitText(t, term, "the count on the rail", "4 subagents")
	saveFrame(t, term, "subagent-list-launched")

	// A client push keeps the list: making a window makes the client push.
	if out, err := tuiosCLI(t, base, "new-window", "second", "-s", "e2e-sub"); err != nil {
		t.Fatalf("make a second window: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, uiTimeout); err != nil {
		t.Fatalf("the second window never appeared: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(time.Second)
	if after := readSubagents(t, base, "e2e-sub"); len(after.List) != 4 || after.byID(saTmux).Now != "Bash: go test ./..." {
		t.Fatalf("a client push changed the list:\n%s", after.raw)
	}
	log.add("after a client push: the list holds 4")

	if old := os.Getenv("TUIOS_E2E_OLD_BIN"); old != "" {
		prev := tuiosBin
		tuiosBin = old
		oldTerm := attachIn(t, base, "e2e-sub", startOpts{})
		tuiosBin = prev
		waitText(t, oldTerm, "the count on an older client's rail", "4 subagents")
		saveFrame(t, oldTerm, "subagent-list-old-client")
		log.add("an older client draws the count")
	}

	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-sub", "-w", "0", "\n"); err != nil {
		t.Fatalf("step the fake claude: %v\n%s", err, out)
	}
	waitCapture(t, base, "e2e-sub", "0", "FAKE-AT finished")
	st = waitSubagents(t, base, "e2e-sub", log, "finished", func(s subagentsOf) bool {
		return s.Subagents == 0 && len(s.List) == 4 && s.byID(saRail).Tools == 2 && s.State == "done"
	})
	checks := []struct {
		id, state, result, last string
		tools                   int
	}{
		{saTmux, "done", "tmux keeps every pane in one server.", "Bash: go test ./...", 1},
		{saDocs, "stopped", "", "Edit: AGENTS.md", 1},
		{saRail, "done", "The rail is fine.", "Bash: go vet ./...", 2},
		{saCI, "failed", "Agent failed: the API is rate limited", "Bash: gh run list", 1},
	}
	for _, c := range checks {
		r := st.byID(c.id)
		if r.State != c.state || r.Result != c.result || r.Last != c.last || r.Tools != c.tools || r.Now != "" || r.EndedAt < r.StartedAt || r.EndedAt == 0 {
			t.Fatalf("%s is %+v, want %s with result %q, last %q, %d tools", c.id, r, c.state, c.result, c.last, c.tools)
		}
	}
	if st.Message != "Three agents reported back." {
		t.Fatalf("the pane's message is %q", st.Message)
	}
	saveFrame(t, term, "subagent-list-finished")

	out, err := tuiosCLI(t, base, "agent-log", "-s", "e2e-sub", "-w", "0")
	if err != nil {
		t.Fatalf("agent-log: %v\n%s", err, out)
	}
	log.add("agent-log:\n%s", out)
	for _, want := range []string{
		"subagent  Research tmux (general-purpose) started",
		"subagent  Research tmux (general-purpose) done after",
		"subagent  Write the docs (general-purpose) stopped",
		"subagent  Check CI (Explore) failed",
		"subagent  Audit the rail (general-purpose) done after",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("agent-log does not say %q:\n%s", want, out)
		}
	}
	// The ring keeps each start and each end, and none of the subagents'
	// own tool calls.
	if strings.Contains(out, "go vet") || strings.Count(out, "subagent  ") != 8 {
		t.Fatalf("agent-log holds the subagents' tool calls, or misses a start or an end:\n%s", out)
	}

	// The fade: done and stopped go after 2 s, the failed one after 10.
	waitSubagents(t, base, "e2e-sub", log, "faded", func(s subagentsOf) bool {
		return len(s.List) == 1 && s.List[0].ID == saCI && s.List[0].State == "failed"
	})
	deadline := time.Now().Add(15 * time.Second)
	for len(readSubagents(t, base, "e2e-sub").List) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the failed subagent never faded:\n%s", readSubagents(t, base, "e2e-sub").raw)
		}
		time.Sleep(200 * time.Millisecond)
	}
	log.add("the failed subagent faded")
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-sub", "-w", "0", "\n"); err != nil {
		t.Fatalf("end the fake claude: %v\n%s", err, out)
	}
	waitCapture(t, base, "e2e-sub", "0", "FAKE-DONE")
	alive(t, term, "after the subagents came and went")
}
