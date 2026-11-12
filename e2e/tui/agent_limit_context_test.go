package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// A lead that runs workers needs two things the screen of each worker shows
// and tuios did not: that a worker stopped on its usage limit, and how full
// each worker's context is. These tests run the real hook, the real status
// line wrapper and a real transcript join against a real daemon and client,
// and keep the rail and the agents view as frames.

// limitRow is the part of a list-agents row these tests read.
type limitRow struct {
	WindowID    string `json:"window_id"`
	State       string `json:"state"`
	Message     string `json:"message"`
	NeedsYou    bool   `json:"needs_you"`
	Ready       bool   `json:"ready"`
	LimitResets string `json:"limit_resets"`
	Context     *struct {
		Tokens  int64 `json:"tokens"`
		Window  int64 `json:"window"`
		Percent *int  `json:"percent"`
		Near    bool  `json:"near"`
	} `json:"context"`
}

// firstWindowID is the id of the session's first window.
func firstWindowID(t *testing.T, base, session string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "--json", "-s", session)
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var res struct {
		Windows []struct {
			ID string `json:"window_id"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res.Windows) == 0 {
		t.Fatalf("list-windows --json: %v\n%s", err, out)
	}
	return res.Windows[0].ID
}

// listAgentRow is the list-agents --json row of window in session, or its
// first row for an empty window.
func listAgentRow(t *testing.T, base, session, window string) limitRow {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-agents", "--json", "-s", session)
	if err != nil {
		t.Fatalf("list-agents: %v\n%s", err, out)
	}
	var res struct {
		Agents []limitRow `json:"agents"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("list-agents --json: %v\n%s", err, out)
	}
	for _, a := range res.Agents {
		if window == "" || a.WindowID == window {
			return a
		}
	}
	t.Fatalf("list-agents has no row for %s:\n%s", window, out)
	return limitRow{}
}

// runHookFile runs the real Claude Code hook in the pane's shell with the
// payload in a file, the way Claude Code runs it, and waits for it to end.
func runHookFile(t *testing.T, base, session, payload, marker string) {
	t.Helper()
	file := filepath.Join(base, marker+".json")
	if err := os.WriteFile(file, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	line := tuiosBin + " agent-hook claude-code < '" + file + "'; echo " + marker + "\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", session, line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	waitCapture(t, base, session, "0", marker)
}

// TestLimitReachedReadsAsItsOwnState: Claude Code ends a turn on its usage
// limit with a StopFailure whose error is rate_limit. The pane reads
// limit_reached (not errored, not done, not needing the person) in
// list-agents with the reset Claude Code named, the rail says "limit
// reached", and the agents view says "Limit reached" with the harness's line.
// An overloaded StopFailure stays errored, now with its real error, and the
// older report, errored with kind usage_limit, lands as limit_reached.
//
// How it could pass wrongly: the hook could read limit_reached from any
// StopFailure, so the overloaded control must stay errored; and the rail word
// could come from the message, so the message has no "limit reached" in it.
//
// Negative controls (NEGATIVE_CONTROLS.md, TestLimitReachedReadsAsItsOwnState).
func TestLimitReachedReadsAsItsOwnState(t *testing.T) {
	term, base := attachClientBase(t)
	const session = "e2e-ctrlp"
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", session, "working", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	runHookFile(t, base, session, `{"hook_event_name":"StopFailure","session_id":"lim-1","error":"rate_limit","error_details":"429 Too Many Requests","last_assistant_message":"You've hit your session limit · resets 11:10am (Australia/Sydney)"}`, "LIMIT-HOOK-DONE")
	log := &stateLog{name: "limit-reached"}
	defer log.save(t)
	st := waitAgentState(t, base, session, "0", "limit_reached", "", log, "StopFailure rate_limit")
	if st.Message != "You've hit your session limit · resets 11:10am (Australia/Sydney)" {
		t.Fatalf("ASSERTION: the limit's message is not Claude Code's line: %+v", st)
	}
	row := listAgentRow(t, base, session, "")
	if row.State != "limit_reached" || row.LimitResets != "11:10am (Australia/Sydney)" || row.NeedsYou || !row.Ready {
		t.Fatalf("ASSERTION: list-agents row is %+v, want limit_reached, resets 11:10am (Australia/Sydney), not needing the person, ready", row)
	}
	log.add("list-agents state=%s limit_resets=%q needs_you=%v ready=%v", row.State, row.LimitResets, row.NeedsYou, row.Ready)

	showRail(t, term)
	waitForAll(t, term, uiTimeout, "the rail row says limit reached", "limit reached")
	saveFrame(t, term, "limit-reached-rail")

	// The leader works from terminal mode; a toast can take the place of
	// the mode's own before a wait for it reads it.
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitForAll(t, term, uiTimeout, "the agents view says limit reached", "Limit reached", "resets 11:10am")
	saveCockpitFrames(t, term, "limit-reached-agents-view")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close the agents view: %v", err)
	}

	// Control: the service being busy is not the person's limit.
	runHookFile(t, base, session, `{"hook_event_name":"StopFailure","session_id":"lim-1","error":"overloaded","last_assistant_message":"API Error: Overloaded"}`, "OVERLOADED-HOOK-DONE")
	st = waitAgentState(t, base, session, "0", "errored", "", log, "StopFailure overloaded")
	if st.Message != "stopped on overloaded" {
		t.Fatalf("ASSERTION: an overloaded stop says %q, want the error it named", st.Message)
	}

	// The older report of a limit is the same state.
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", session, "errored", "--kind", "usage_limit", "-m", "usage limit reached"); err != nil {
		t.Fatalf("set-agent-state errored --kind usage_limit: %v\n%s", err, out)
	}
	waitAgentState(t, base, session, "0", "limit_reached", "", log, "errored --kind usage_limit")
	alive(t, term, "after the limit reports")
}

// limitAgentScript is a stand-in for Claude Code with no hooks installed, run
// through fakeclaude so the pane's program is claude and only its screen
// says what it does. It draws its input box at rest, then on the first line
// typed the limit line above the box, then on the second line a later answer
// under the limit line, as after the reset. $1 is the file it writes when it
// has drawn the last screen.
const limitAgentScript = `R=$(stty size | cut -d' ' -f1)
RULE="────────────────────────────────────────────"
box() {
  printf '\033[H\033[2J'
  i=1
  for l in "$@"; do printf '\033[%d;1H%s' "$i" "$l"; i=$((i+1)); done
  printf '\033[%d;1H%s\033[%d;1H❯ \033[%d;1H%s\033[%d;1H  ? for shortcuts' $((R-3)) "$RULE" $((R-2)) $((R-1)) "$RULE" "$R"
}
printf '\033]0;\342\234\263 Claude Code\007\033[?1049h'
box "⏺ Ready."
IFS= read -r l
box "⏺ I will run the suite next." "" " ⎿  You've hit your session limit · resets 11:10am (Australia/Sydney)"
IFS= read -r l
box " ⎿  You've hit your session limit · resets 11:10am (Australia/Sydney)" "" "> go on" "" "⏺ The suite passes."
echo LATER >"$1"
sleep 600
`

// TestLimitReachedFromTheScreen: a Claude Code with no hooks shows its limit
// line above its input box. The screen rule reads limit_reached with the
// reset, and once a later answer sits under the old limit line the pane is at
// rest again, not held on the limit.
//
// How it could pass wrongly: the limit could be read from the rail word of a
// report, so the stand-in runs no hook and the source must be screen; and a
// rule reading the whole screen would keep limit_reached after the answer,
// which the last step refuses.
//
// Negative controls (NEGATIVE_CONTROLS.md, TestLimitReachedFromTheScreen).
func TestLimitReachedFromTheScreen(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	t.Cleanup(func() { dumpDaemonLogOnFail(t, base) })
	const session = "e2e-limit-screen"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", filepath.Join(bin, "claude"), "./testdata/fakeclaude").CombinedOutput(); err != nil {
		t.Fatalf("build fakeclaude: %v\n%s", err, out)
	}
	script := filepath.Join(base, "limit-agent.sh")
	if err := os.WriteFile(script, []byte(limitAgentScript), 0o755); err != nil {
		t.Fatal(err)
	}
	later := filepath.Join(base, "later")
	if out, err := tuiosCLI(t, base, "new-window", "agent", "-s", session, "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	agent := windowID(t, base, session, "agent")
	if out, err := tuiosCLI(t, base, "call", "resize", `{"session":"`+session+`","window":"`+agent+`","width":120,"height":30}`); err != nil {
		t.Fatalf("resize: %v\n%s", err, out)
	}
	log := &stateLog{name: "limit-reached-screen"}
	defer log.save(t)
	start := filepath.Join(bin, "claude") + " '" + script + "' '" + later + "'\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", agent, start); err != nil {
		t.Fatalf("start the stand-in: %v\n%s", err, out)
	}
	waitCapture(t, base, session, agent, "Ready.")
	waitAgentState(t, base, session, agent, "idle", "", log, "stand-in at rest")

	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", agent, "run the suite\n"); err != nil {
		t.Fatalf("send the prompt: %v\n%s", err, out)
	}
	st := waitAgentState(t, base, session, agent, "limit_reached", "", log, "limit line above the box")
	if st.Source != "screen" || st.Message != "You've hit your session limit · resets 11:10am (Australia/Sydney)" || st.BlockedBy != "" {
		t.Fatalf("ASSERTION: the limit is not the screen's, with the reset: %+v", st)
	}
	if row := listAgentRow(t, base, session, agent); row.LimitResets != "11:10am (Australia/Sydney)" {
		t.Fatalf("ASSERTION: list-agents limit_resets is %q", row.LimitResets)
	}
	snap, _ := tuiosCLI(t, base, "capture-pane", "-s", session, "-w", agent)
	log.add("screen at the limit:\n%s", snap)

	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", agent, "go on\n"); err != nil {
		t.Fatalf("send the second prompt: %v\n%s", err, out)
	}
	waitUntil(t, "the stand-in's later answer", uiTimeout, func() (bool, string) {
		_, err := os.Stat(later)
		return err == nil, "the stand-in has not drawn the later answer"
	})
	waitAgentState(t, base, session, agent, "idle", "", log, "a later answer under the old limit line")
}

// TestContextSizeReachesListAgentsAndTheAgentsView: a joined Claude Code
// transcript gives the pane's context tokens with no window; the status line
// feed then states the window, and past 80 percent of it list-agents says
// near and the agents view draws "near" in the warn ink.
//
// How it could pass wrongly: the window could be guessed from the model, so
// the transcript step must show tokens with no window and no percent; and
// near could be read from the status line's own percent, so the last payload
// gives a percent under 80 that the tokens and window put over it.
//
// Negative controls (NEGATIVE_CONTROLS.md, TestContextSizeReachesListAgentsAndTheAgentsView).
func TestContextSizeReachesListAgentsAndTheAgentsView(t *testing.T) {
	term, base := attachClientBase(t)
	const session = "e2e-ctrlp"
	win := firstWindowID(t, base, session)
	dir := filepath.Join(atProjects(base), "ctx")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ctx-1.jsonl")
	record := func(uuid string, input, cacheRead int64) string {
		b, _ := json.Marshal(map[string]any{
			"type": "assistant", "uuid": uuid, "timestamp": "2026-11-12T10:00:00.000Z", "isSidechain": false, "sessionId": "ctx-1",
			"message": map[string]any{
				"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "The suite passes."}}, "stop_reason": "end_turn",
				"usage": map[string]any{"input_tokens": input, "cache_creation_input_tokens": 0, "cache_read_input_tokens": cacheRead, "output_tokens": 50},
			},
		})
		return string(b)
	}
	if err := os.WriteFile(path, []byte(record("c1", 12, 600000)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", session, "--window", win)
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	payload, _ := json.Marshal(map[string]any{"hook_event_name": "SessionStart", "session_id": "ctx-1", "transcript_path": path, "source": "startup"})
	hook.Stdin = strings.NewReader(string(payload))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
	// The join reads the file it is given; a record appended after shows
	// the reader follows the file.
	appendLines(t, path, record("c2", 12, 612000))
	art := &stateLog{name: "context-size"}
	defer art.save(t)
	var row limitRow
	waitUntil(t, "list-agents gives the transcript's tokens", uiTimeout, func() (bool, string) {
		row = listAgentRow(t, base, session, win)
		return row.Context != nil && row.Context.Tokens == 612012, "context is " + contextText(row)
	})
	if row.Context.Window != 0 || row.Context.Percent != nil || row.Context.Near {
		t.Fatalf("ASSERTION: the transcript alone gave a window or a percent: %s", contextText(row))
	}
	art.add("transcript only: %s", contextText(row))

	// The status line states the window. Its own percent says 79; the
	// tokens over the window are 85 percent, which is near.
	status := `{"session_id":"ctx-1","model":{"display_name":"Opus"},"context_window":{"used_percentage":79,"context_window_size":1000000,"current_usage":{"input_tokens":12,"cache_creation_input_tokens":0,"cache_read_input_tokens":851000,"output_tokens":40}}}`
	file := filepath.Join(base, "status.json")
	if err := os.WriteFile(file, []byte(status), 0o600); err != nil {
		t.Fatal(err)
	}
	line := tuiosBin + " agent-statusline claude-code < '" + file + "'; echo STATUS-DONE\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", session, line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	waitCapture(t, base, session, "0", "STATUS-DONE")
	waitUntil(t, "list-agents gives the window and near", uiTimeout, func() (bool, string) {
		row = listAgentRow(t, base, session, win)
		return row.Context != nil && row.Context.Window == 1000000 && row.Context.Near, "context is " + contextText(row)
	})
	if row.Context.Tokens != 851012 || row.Context.Percent == nil || *row.Context.Percent != 85 {
		t.Fatalf("ASSERTION: context is %s, want 851012 of 1000000, 85 percent", contextText(row))
	}
	art.add("with the status line: %s", contextText(row))

	// The leader works from terminal mode; a toast can take the place of
	// the mode's own before a wait for it reads it.
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitForAll(t, term, uiTimeout, "the agents view's context column", "ctx 851k/1M near")
	// On the row as well as in the focus header: the row is the column a
	// lead scans.
	if row := fleetRowWith(term.Screen(), "claude"); row < 0 || !strings.Contains(string([]rune(term.Screen().Line(row))[:50]), "851k/1M near") {
		t.Fatalf("ASSERTION: the agent's row has no context column\n%s", term.Snapshot())
	}
	saveCockpitFrames(t, term, "context-size-agents-view")
	alive(t, term, "after the context size")
}

// contextText is a list-agents row's context, for a failure message.
func contextText(r limitRow) string {
	if r.Context == nil {
		return "null"
	}
	b, _ := json.Marshal(r.Context)
	return string(b)
}
