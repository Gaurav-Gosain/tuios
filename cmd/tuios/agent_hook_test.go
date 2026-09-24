package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// fakeDaemon records the verbs a hook calls and answers them.
type fakeDaemon struct {
	calls []fakeCall
	// old stands in for a daemon that predates the hook fields. Like the real
	// one it decodes params leniently: set-agent-state ignores a field it does
	// not know and applies the report anyway, and list-verbs does not list the
	// hook fields.
	old bool
	// noActivity stands in for a daemon that has every hook field but
	// activity: the daemons from before the activity ring.
	noActivity bool
	resolved   map[string]any
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
		if p["verb"] == "set-agent-session" {
			if f.old {
				return json.RawMessage(`{"verbs":[]}`), nil
			}
			return json.RawMessage(`{"verbs":[{"verb":"set-agent-session"}]}`), nil
		}
		names := oldSetAgentStateParams
		if !f.old {
			names = append([]string(nil), names...)
			for _, n := range hookFields {
				if n != "activity" || !f.noActivity {
					names = append(names, n)
				}
			}
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
	case "set-agent-session":
		if f.old {
			break
		}
		return json.RawMessage(`{"applied":true,"agent_session_id":"` + p["agent_session_id"].(string) + `"}`), nil
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

// TestAgentHookSendsActivity: a tool call's activity rides its state report
// to a daemon that lists the field, and a daemon from before it gets the
// report without it, so the state is never lost.
func TestAgentHookSendsActivity(t *testing.T) {
	payload := `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`
	h := &hookRun{env: map[string]string{"TUIOS_PANE_ID": "w1"}}
	h.run(t, agentHookOptions{}, payload, "claude-code")
	r := h.daemon.reports()
	if len(r) != 1 {
		t.Fatalf("reports = %v", r)
	}
	activity, _ := r[0]["activity"].(map[string]any)
	if activity["event"] != "tool" || activity["tool"] != "Bash" || activity["target"] != "go test ./..." {
		t.Fatalf("activity = %v", r[0]["activity"])
	}

	h = &hookRun{env: map[string]string{"TUIOS_PANE_ID": "w1"}, daemon: &fakeDaemon{noActivity: true}}
	h.run(t, agentHookOptions{}, payload, "claude-code")
	r = h.daemon.reports()
	if len(r) != 1 || r[0]["state"] != "working" || r[0]["activity"] != nil || r[0]["agent_session_id"] != "s1" {
		t.Fatalf("reports to a daemon without activity = %v", r)
	}
	if !strings.Contains(h.stderr.String(), `"unsupported":["activity"]`) {
		t.Fatalf("explain does not name the dropped field: %s", h.stderr.String())
	}
}

// TestAgentHookStopSaysWhatTheTurnEndedOn: the done report carries the first
// line of what the agent said last, as its message and as activity.
func TestAgentHookStopSaysWhatTheTurnEndedOn(t *testing.T) {
	h := &hookRun{env: map[string]string{"TUIOS_PANE_ID": "w1"}}
	h.run(t, agentHookOptions{}, `{"hook_event_name":"Stop","session_id":"s1","last_assistant_message":"All tests pass.\nHere is why."}`, "claude-code")
	r := h.daemon.reports()
	if len(r) != 1 || r[0]["state"] != "done" || r[0]["message"] != "All tests pass." {
		t.Fatalf("reports = %v", r)
	}
	if a, _ := r[0]["activity"].(map[string]any); a["event"] != "turn_end" || a["text"] != "All tests pass." {
		t.Fatalf("activity = %v", r[0]["activity"])
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

// TestAgentHookSendsASessionOnlyReport checks an identity integration's event
// goes to set-agent-session, with the harness pid, and never to
// set-agent-state, so the pane's state is left to its screen rules.
func TestAgentHookSendsASessionOnlyReport(t *testing.T) {
	h := &hookRun{env: map[string]string{"TUIOS_PANE_ID": "w1", "TUIOS_SESSION": "work"}}
	h.run(t, agentHookOptions{}, `{"hook_event_name":"SessionStart","session_id":"qw-1","source":"startup"}`, "qwen")
	if r := h.daemon.reports(); len(r) != 0 {
		t.Fatalf("an identity event reported a state: %v", r)
	}
	var sent map[string]any
	for _, c := range h.daemon.calls {
		if c.verb == "set-agent-session" {
			sent = c.params
		}
	}
	want := map[string]any{"session": "work", "window": "w1", "harness": "qwen", "agent_session_id": "qw-1", "harness_pid": float64(4250)}
	for k, v := range want {
		if sent[k] != v {
			t.Errorf("%s = %v, want %v (calls %v)", k, sent[k], v, h.daemon.calls)
		}
	}
	if h.stdout.Len() != 0 {
		t.Errorf("printed %q on stdout", h.stdout.String())
	}
	if !strings.Contains(h.stderr.String(), `"applied":true`) {
		t.Errorf("explain: %s", h.stderr.String())
	}

	// A daemon without the verb gets nothing, and --explain says why.
	h = &hookRun{env: map[string]string{"TUIOS_PANE_ID": "w1"}, daemon: &fakeDaemon{old: true}}
	h.run(t, agentHookOptions{}, `{"hook_event_name":"SessionStart","session_id":"qw-1"}`, "qwen")
	for _, c := range h.daemon.calls {
		if c.verb == "set-agent-session" || c.verb == "set-agent-state" {
			t.Fatalf("reported to a daemon without set-agent-session: %v", h.daemon.calls)
		}
	}
	if !strings.Contains(h.stderr.String(), "predates set-agent-session") {
		t.Fatalf("explain: %s", h.stderr.String())
	}
}

// TestAgentHookAnswersAntigravityWithAnObject checks the one other harness
// that parses a hook's stdout gets the empty object, even when it reports.
func TestAgentHookAnswersAntigravityWithAnObject(t *testing.T) {
	h := &hookRun{env: map[string]string{"TUIOS_PANE_ID": "w1"}}
	h.run(t, agentHookOptions{}, `{"hook_event_name":"PreInvocation","conversationId":"ag-1"}`, "antigravity")
	if h.stdout.String() != "{}\n" {
		t.Fatalf("stdout = %q, want an empty object", h.stdout.String())
	}
}
