package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// planDaemon is a holdDaemon whose request-approval lists the params named in
// takes, the way list-verbs describes it.
type planDaemon struct {
	holdDaemon
	takes []string
}

func (p *planDaemon) Call(verb string, params any) (json.RawMessage, error) {
	return p.CallWithTimeout(verb, params, 30*time.Second)
}

func (p *planDaemon) CallWithTimeout(verb string, params any, timeout time.Duration) (json.RawMessage, error) {
	if verb == "list-verbs" {
		raw, _ := json.Marshal(params)
		var q map[string]any
		_ = json.Unmarshal(raw, &q)
		if q["verb"] == "request-approval" {
			var ps []map[string]string
			for _, n := range append([]string{"session", "window", "harness", "options", "summary", "always_scope"}, p.takes...) {
				ps = append(ps, map[string]string{"name": n})
			}
			out, _ := json.Marshal(map[string]any{"verbs": []any{map[string]any{"verb": "request-approval", "params": ps}}})
			return out, nil
		}
	}
	return p.holdDaemon.CallWithTimeout(verb, params, timeout)
}

var requestApprovalFields = []string{"kind", "plan", "tool", "target", "deny_message"}

const claudePlan = `{"hook_event_name":"PermissionRequest","session_id":"s1","tool_name":"ExitPlanMode","tool_input":{"plan":"# Refactor retry\n1. Move backoff.","planFilePath":"/p/retry.md"}}`

// TestAgentHookLeavesAPlanToAnOlderDaemon: a daemon whose request-approval
// does not list plan would hold the plan as a plain approval, answerable
// without the plan ever shown, so the hook does not ask it at all.
func TestAgentHookLeavesAPlanToAnOlderDaemon(t *testing.T) {
	d := &planDaemon{}
	d.answer = answerWith("once", "answered", "")
	out, explain := runHold(t, &d.holdDaemon, func() (verbCaller, error) { return d, nil }, "claude-code", claudePlan, 0)
	if out != "" {
		t.Errorf("printed %q to an older daemon", out)
	}
	if len(d.holdCalls()) != 0 {
		t.Errorf("request-approval was called: %v", d.holdCalls())
	}
	if !strings.Contains(explain, "predates plans") {
		t.Errorf("explain does not say why: %s", explain)
	}
}

func TestAgentHookNamesToolAndTarget(t *testing.T) {
	d := &planDaemon{takes: requestApprovalFields}
	d.answer = answerWith("once", "answered", "")
	_, _ = runHold(t, &d.holdDaemon, func() (verbCaller, error) { return d, nil }, "claude-code", claudeBash, 0)
	h := d.holdCalls()
	if len(h) != 1 || h[0]["tool"] != "Bash" || h[0]["target"] != "go test ./..." || h[0]["deny_message"] != true {
		t.Fatalf("request-approval params %v", h)
	}
	if _, ok := h[0]["kind"]; ok {
		t.Errorf("an approval sent kind: %v", h[0])
	}

	// A daemon that takes only some of them gets only those.
	d = &planDaemon{takes: []string{"tool"}}
	d.answer = answerWith("once", "answered", "")
	_, _ = runHold(t, &d.holdDaemon, func() (verbCaller, error) { return d, nil }, "claude-code", claudeBash, 0)
	h = d.holdCalls()
	if len(h) != 1 || h[0]["tool"] != "Bash" {
		t.Fatalf("request-approval params %v", h)
	}
	for _, k := range []string{"target", "deny_message"} {
		if _, ok := h[0][k]; ok {
			t.Errorf("sent %s to a daemon that does not list it: %v", k, h[0])
		}
	}
}
