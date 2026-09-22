package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// fakeDaemon records the verbs a hook calls and answers them.
type fakeDaemon struct {
	calls []fakeCall
	// old stands in for a daemon that predates the hook fields. Like the real
	// one it decodes params leniently: set-agent-state ignores a field it does
	// not know and applies the report anyway, and list-verbs does not list the
	// hook fields.
	old      bool
	resolved map[string]any
}

// oldSetAgentStateParams are the params set-agent-state took before the hook
// fields, as list-verbs described them at 1c58fc7d.
var oldSetAgentStateParams = []string{"session", "window", "state", "message", "source", "harness"}

type fakeCall struct {
	verb   string
	params map[string]any
}

func (f *fakeDaemon) Call(verb string, params any) (json.RawMessage, error) {
	raw, _ := json.Marshal(params)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	f.calls = append(f.calls, fakeCall{verb, p})
	switch verb {
	case "resolve-pane":
		if f.resolved == nil {
			return nil, &session.VerbCallError{Code: session.ErrVerbWindowNotFound, Message: "no pane"}
		}
		out, _ := json.Marshal(f.resolved)
		return out, nil
	case "list-verbs":
		names := oldSetAgentStateParams
		if !f.old {
			names = append(append([]string(nil), names...), hookFields...)
		}
		var ps []map[string]string
		for _, n := range names {
			ps = append(ps, map[string]string{"name": n})
		}
		out, _ := json.Marshal(map[string]any{"verbs": []any{map[string]any{"verb": "set-agent-state", "params": ps}}})
		return out, nil
	case "set-agent-state":
		// Both kinds of daemon apply the report. The old one never looks at
		// the hook fields, so if_state does not stop it.
		return json.RawMessage(`{"applied":true,"state":"` + p["state"].(string) + `"}`), nil
	}
	return nil, &session.VerbCallError{Code: session.ErrVerbUnknownVerb, Message: verb}
}

func (f *fakeDaemon) reports() []map[string]any {
	var out []map[string]any
	for _, c := range f.calls {
		if c.verb == "set-agent-state" {
			out = append(out, c.params)
		}
	}
	return out
}

type hookRun struct {
	env      map[string]string
	daemon   *fakeDaemon
	stdout   bytes.Buffer
	stderr   bytes.Buffer
	dialed   bool
	dialWait time.Duration
}

func (h *hookRun) run(t *testing.T, o agentHookOptions, payload string, args ...string) {
	t.Helper()
	if h.daemon == nil {
		h.daemon = &fakeDaemon{}
	}
	o.explain = true
	runAgentHook(o, args, agentHookIO{
		stdin:  strings.NewReader(payload),
		stdout: &h.stdout,
		stderr: &h.stderr,
		getenv: func(k string) string { return h.env[k] },
		dial: func() (verbCaller, error) {
			h.dialed = true
			time.Sleep(h.dialWait)
			return h.daemon, nil
		},
		self:       func() (int, []int) { return 4242, []int{4250, 4242} },
		harnessPID: func(ancestors []int) int { return ancestors[0] },
	})
}

func TestAgentHookReportsToThePaneInTheEnvironment(t *testing.T) {
	h := &hookRun{env: map[string]string{"TUIOS_PANE_ID": "w7", "TUIOS_SESSION": "work"}}
	h.run(t, agentHookOptions{}, `{"hook_event_name":"PermissionRequest","session_id":"s1","transcript_path":"/t.jsonl","tool_name":"Bash","tool_input":{"command":"make"}}`, "claude-code")

	reports := h.daemon.reports()
	if len(reports) != 1 {
		t.Fatalf("reports = %v, stderr %s", reports, h.stderr.String())
	}
	want := map[string]any{
		"session": "work", "window": "w7", "state": "needs_input", "harness": "claude-code",
		"kind": "approval", "message": "approve Bash: make", "agent_session_id": "s1", "transcript_path": "/t.jsonl",
	}
	for k, v := range want {
		if reports[0][k] != v {
			t.Errorf("%s = %v, want %v", k, reports[0][k], v)
		}
	}
	if h.stdout.Len() != 0 {
		t.Errorf("printed %q on stdout, which Claude Code would read as a decision", h.stdout.String())
	}
	if !strings.Contains(h.stderr.String(), `"pane_by":"env"`) {
		t.Errorf("explain output: %s", h.stderr.String())
	}
}

func TestAgentHookWindowFlagWins(t *testing.T) {
	h := &hookRun{env: map[string]string{"TUIOS_PANE_ID": "w7"}}
	h.run(t, agentHookOptions{window: "w9", session: "other"}, `{"hook_event_name":"Stop","session_id":"s1"}`, "claude")
	r := h.daemon.reports()
	if len(r) != 1 || r[0]["window"] != "w9" || r[0]["session"] != "other" || r[0]["state"] != "done" {
		t.Fatalf("reports = %v", r)
	}
}

// TestAgentHookFindsThePaneWithoutItsEnvironment is the scrubbed-environment
// case: no TUIOS_PANE_ID, so the process's terminal session and ancestors go
// to resolve-pane.
func TestAgentHookFindsThePaneWithoutItsEnvironment(t *testing.T) {
	h := &hookRun{daemon: &fakeDaemon{resolved: map[string]any{"session": "work", "window_id": "w3", "by": "tty"}}}
	h.run(t, agentHookOptions{}, `{"hook_event_name":"UserPromptSubmit","session_id":"s1"}`, "claude-code")

	if len(h.daemon.calls) != 3 || h.daemon.calls[0].verb != "resolve-pane" {
		t.Fatalf("calls = %v", h.daemon.calls)
	}
	if sid := h.daemon.calls[0].params["sid"]; sid != float64(4242) {
		t.Fatalf("resolve-pane sid = %v", sid)
	}
	r := h.daemon.reports()
	if r[0]["window"] != "w3" || r[0]["session"] != "work" {
		t.Fatalf("report went to %v", r[0])
	}
}

func TestAgentHookReportsNothingWithoutAPane(t *testing.T) {
	h := &hookRun{}
	h.run(t, agentHookOptions{}, `{"hook_event_name":"Stop","session_id":"s1"}`, "claude-code")
	if len(h.daemon.reports()) != 0 {
		t.Fatalf("reported with no pane: %v", h.daemon.calls)
	}
	if !strings.Contains(h.stderr.String(), "no pane") {
		t.Fatalf("explain: %s", h.stderr.String())
	}
}

func TestAgentHookDoesNotDialForAnEventItSkips(t *testing.T) {
	h := &hookRun{env: map[string]string{"TUIOS_PANE_ID": "w1"}}
	h.run(t, agentHookOptions{}, `{"hook_event_name":"SubagentStop","agent_id":"a"}`, "claude-code")
	if h.dialed {
		t.Fatal("dialed the daemon for an event that reports nothing")
	}
	h = &hookRun{env: map[string]string{"TUIOS_PANE_ID": "w1"}}
	h.run(t, agentHookOptions{}, `garbage`, "claude-code", "Stop")
	if h.dialed {
		t.Fatal("a payload that does not parse was reported")
	}
}

// TestAgentHookHandlesAnOldDaemon checks a daemon that predates the hook
// fields. Such a daemon does not refuse them: it decodes params leniently and
// applies the report without them. So the hook asks list-verbs first, sends
// only what the daemon knows, and does not send a conditional report at all,
// since applied without its if_state it would turn done back to working.
func TestAgentHookHandlesAnOldDaemon(t *testing.T) {
	h := &hookRun{env: map[string]string{"TUIOS_PANE_ID": "w1"}, daemon: &fakeDaemon{old: true}}
	h.run(t, agentHookOptions{}, `{"hook_event_name":"Stop","session_id":"s1"}`, "claude-code")
	r := h.daemon.reports()
	if len(r) != 1 || r[0]["state"] != "done" || r[0]["agent_session_id"] != nil || r[0]["harness_pid"] != nil {
		t.Fatalf("reports = %v", r)
	}
	if !strings.Contains(h.stderr.String(), `"unsupported":["agent_session_id","harness_pid"]`) {
		t.Fatalf("explain does not name the dropped fields: %s", h.stderr.String())
	}

	// PostToolUse (working if needs_input) and idle_prompt (idle if working or
	// unknown) are both conditional.
	for _, payload := range []string{
		`{"hook_event_name":"PostToolUse","session_id":"s1"}`,
		`{"hook_event_name":"Notification","notification_type":"idle_prompt","session_id":"s1"}`,
	} {
		h = &hookRun{env: map[string]string{"TUIOS_PANE_ID": "w1"}, daemon: &fakeDaemon{old: true}}
		h.run(t, agentHookOptions{}, payload, "claude-code")
		if r := h.daemon.reports(); len(r) != 0 {
			t.Fatalf("%s: a conditional report went to a daemon that would drop its condition: %v", payload, r)
		}
		if !strings.Contains(h.stderr.String(), "predates if_state") {
			t.Fatalf("explain: %s", h.stderr.String())
		}
	}

	// A current daemon gets the condition and the harness pid.
	h = &hookRun{env: map[string]string{"TUIOS_PANE_ID": "w1"}}
	h.run(t, agentHookOptions{}, `{"hook_event_name":"PostToolUse","session_id":"s1"}`, "claude-code")
	r = h.daemon.reports()
	if len(r) != 1 || r[0]["if_state"] == nil || r[0]["harness_pid"] != float64(4250) {
		t.Fatalf("reports = %v", r)
	}
}

// TestRequireIfState checks tuios set-agent-state --if-state refuses to send to
// a daemon that would ignore the condition and apply the report anyway.
func TestRequireIfState(t *testing.T) {
	if err := requireIfState(&fakeDaemon{old: true}); err == nil || !strings.Contains(err.Error(), "predates --if-state") {
		t.Fatalf("an old daemon: %v", err)
	}
	if err := requireIfState(&fakeDaemon{}); err != nil {
		t.Fatalf("a current daemon: %v", err)
	}
}

func TestAgentHookGivesUpAtTheDeadline(t *testing.T) {
	h := &hookRun{env: map[string]string{"TUIOS_PANE_ID": "w1"}, dialWait: 2 * time.Second}
	start := time.Now()
	h.run(t, agentHookOptions{timeout: 50 * time.Millisecond}, `{"hook_event_name":"AfterAgent","session_id":"g"}`, "gemini-cli")
	if el := time.Since(start); el > time.Second {
		t.Fatalf("the hook took %v with a stuck daemon", el)
	}
	if h.stdout.String() != "{}\n" {
		t.Fatalf("Gemini CLI got %q on stdout, want an empty object", h.stdout.String())
	}
	if !strings.Contains(h.stderr.String(), "gave up") {
		t.Fatalf("explain: %s", h.stderr.String())
	}
}

func TestAgentHookReadsTheCodexNotifyArgument(t *testing.T) {
	h := &hookRun{env: map[string]string{"TUIOS_PANE_ID": "w1"}}
	h.run(t, agentHookOptions{}, "", "codex", `{"type":"agent-turn-complete","thread-id":"th-9"}`)
	r := h.daemon.reports()
	if len(r) != 1 || r[0]["state"] != "done" || r[0]["agent_session_id"] != "th-9" {
		t.Fatalf("reports = %v", r)
	}
}

func TestAgentHookCommandIsWired(t *testing.T) {
	root := newRootCommand()
	cmd, _, err := root.Find([]string{"agent-hook"})
	if err != nil || cmd.Name() != "agent-hook" {
		t.Fatalf("agent-hook is not a command: %v", err)
	}
	if cmd.Flags().Lookup("integration") == nil {
		t.Fatal("agent-hook does not accept the --integration marker managed entries carry")
	}
	for _, path := range [][]string{{"integration", "install"}, {"integration", "uninstall"}, {"integration", "status"}, {"doctor", "agents"}} {
		if c, _, err := root.Find(path); err != nil || c.Name() != path[len(path)-1] {
			t.Errorf("tuios %s is not a command", strings.Join(path, " "))
		}
	}
}

func TestDoctorAgentsListsPanesWithoutTheirIntegration(t *testing.T) {
	env := integration.Env{
		Home:     t.TempDir(),
		Getenv:   func(string) string { return "" },
		LookPath: func(name string) (string, error) { return "/bin/" + name, nil },
	}
	panes := func() ([]agentPane, bool) {
		return []agentPane{
			{Session: "work", Window: "w1", Name: "claude", Harness: "claude-code"},
			{Session: "work", Window: "w2", Name: "aider", Harness: "aider"},
		}, true
	}
	r := doctorAgents(env, "tuios", panes)
	if len(r.Harnesses) != len(integration.Targets()) || !r.TuiosOnPath || !r.DaemonRunning {
		t.Fatalf("report = %+v", r)
	}
	if len(r.Panes) != 1 || r.Panes[0].Harness != "claude-code" {
		t.Fatalf("panes = %+v, want only the Claude pane (aider has no integration)", r.Panes)
	}
	var out bytes.Buffer
	if err := printDoctorAgents(&out, r, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"claude-code", "not installed", "work:w1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("doctor output lacks %q:\n%s", want, out.String())
		}
	}
}
