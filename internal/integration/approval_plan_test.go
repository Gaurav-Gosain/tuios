package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

// planPayload is an ExitPlanMode PermissionRequest the way the hooks
// reference describes it: tool_input carries the plan and the file Claude Code
// wrote it to.
func planPayload(input map[string]any, suggestions any) map[string]any {
	p := map[string]any{
		"hook_event_name": "PermissionRequest",
		"session_id":      "s1",
		"permission_mode": "plan",
		"tool_name":       "ExitPlanMode",
		"tool_input":      input,
	}
	if suggestions != nil {
		p["permission_suggestions"] = suggestions
	}
	return p
}

const testPlan = "# Refactor the retry loop\n1. Move backoff into api/retry.go.\n2. Add table tests."

func TestExitPlanModeIsAPlan(t *testing.T) {
	input := map[string]any{"plan": testPlan, "planFilePath": "/home/me/.claude/plans/retry.md"}
	d := translatePayload(t, "claude", planPayload(input, nil))
	if d.Report == nil || d.Report.State != "needs_input" || d.Report.Kind != "approval" {
		t.Fatalf("report %+v, want needs_input with kind approval", d.Report)
	}
	if d.Report.Message != "plan: Refactor the retry loop" {
		t.Errorf("message %q, want the plan's title", d.Report.Message)
	}
	a := d.Approval
	if a == nil || !a.IsPlan() || a.Plan != testPlan || !a.DenyMessage {
		t.Fatalf("approval %+v, want a plan with its text and deny_message", a)
	}
	if len(a.Options) != 2 || a.Options[0] != DecisionOnce || a.Options[1] != DecisionDeny || len(a.Scope) != 0 {
		t.Errorf("options %v scope %v, want once and deny with no always", a.Options, a.Scope)
	}

	// Allow hands back the input unchanged as updatedInput, which Claude
	// Code needs for a tool that asks the user.
	out, ok := a.Answer("claude-code", DecisionOnce, "")
	if !ok {
		t.Fatal("once printed nothing")
	}
	var got struct {
		HookSpecificOutput struct {
			HookEventName string `json:"hookEventName"`
			Decision      struct {
				Behavior           string          `json:"behavior"`
				UpdatedInput       map[string]any  `json:"updatedInput"`
				UpdatedPermissions json.RawMessage `json:"updatedPermissions"`
				Message            string          `json:"message"`
			} `json:"decision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	dec := got.HookSpecificOutput.Decision
	if got.HookSpecificOutput.HookEventName != "PermissionRequest" || dec.Behavior != "allow" {
		t.Fatalf("allow printed %s", out)
	}
	if dec.UpdatedInput["plan"] != testPlan || dec.UpdatedInput["planFilePath"] != input["planFilePath"] || len(dec.UpdatedInput) != 2 {
		t.Errorf("updatedInput %v, want the input as it came", dec.UpdatedInput)
	}
	if len(dec.UpdatedPermissions) != 0 {
		t.Errorf("once changed permissions: %s", dec.UpdatedPermissions)
	}
	if _, ok := a.Answer("claude-code", DecisionAlways, ""); ok {
		t.Error("always printed a decision for a plan that was not offered it")
	}

	// Keep planning, with and without a reason.
	out, _ = a.Answer("claude-code", DecisionDeny, "split step 2 in two")
	want := `{"hookSpecificOutput":{"decision":{"behavior":"deny","message":"split step 2 in two"},"hookEventName":"PermissionRequest"}}` + "\n"
	if out != want {
		t.Errorf("deny printed %q, want %q", out, want)
	}
	out, _ = a.Answer("claude-code", DecisionDeny, "")
	if !strings.Contains(out, DefaultPlanDenyMessage) {
		t.Errorf("deny with no reason printed %q", out)
	}
}

func TestPlanAcceptEditsOnlyWhenExactlySuggested(t *testing.T) {
	input := map[string]any{"plan": testPlan}
	accept := map[string]any{"type": "setMode", "mode": "acceptEdits", "destination": "session"}
	for _, tc := range []struct {
		name        string
		suggestions any
		offered     bool
	}{
		{"accept edits for the session", []any{accept}, true},
		{"none", nil, false},
		{"bypassPermissions", []any{map[string]any{"type": "setMode", "mode": "bypassPermissions", "destination": "session"}}, false},
		{"auto", []any{map[string]any{"type": "setMode", "mode": "auto", "destination": "session"}}, false},
		{"accept edits saved to settings", []any{map[string]any{"type": "setMode", "mode": "acceptEdits", "destination": "userSettings"}}, false},
		{"with a rule beside it", []any{accept, map[string]any{"type": "addRules", "behavior": "allow", "destination": "session", "rules": []any{map[string]any{"toolName": "Bash"}}}}, false},
		{"with an unknown field", []any{map[string]any{"type": "setMode", "mode": "acceptEdits", "destination": "session", "extra": true}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := translatePayload(t, "claude", planPayload(input, tc.suggestions)).Approval
			if a == nil {
				t.Fatal("no approval")
			}
			if a.Offers(DecisionAlways) != tc.offered {
				t.Fatalf("always offered = %v, want %v", a.Offers(DecisionAlways), tc.offered)
			}
			if !tc.offered {
				return
			}
			if len(a.Scope) != 1 || a.Scope[0] != AcceptEditsScope {
				t.Errorf("scope %q, want the one mode line", a.Scope)
			}
			out, ok := a.Answer("claude-code", DecisionAlways, "")
			wantPerm := `"updatedPermissions":[{"destination":"session","mode":"acceptEdits","type":"setMode"}]`
			if !ok || !strings.Contains(out, wantPerm) || !strings.Contains(out, `"updatedInput":{"plan":`) || !strings.Contains(out, `"behavior":"allow"`) {
				t.Errorf("always printed %s", out)
			}
		})
	}
}

func TestPlansTheInboxDoesNotHold(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input map[string]any
	}{
		{"no plan", map[string]any{}},
		{"a blank plan", map[string]any{"plan": "  \n "}},
		{"a plan too long", map[string]any{"plan": strings.Repeat("x", MaxPlan+1)}},
		{"allowedPrompts asking for commands", map[string]any{"plan": testPlan, "allowedPrompts": []any{map[string]any{"tool": "Bash", "prompt": "run tests"}}}},
		{"an unknown field", map[string]any{"plan": testPlan, "mode": "auto"}},
		{"a plan that is not a string", map[string]any{"plan": 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := translatePayload(t, "claude", planPayload(tc.input, nil))
			if d.Report == nil {
				t.Fatal("no report")
			}
			if d.Approval != nil {
				t.Errorf("held: %+v", d.Approval)
			}
		})
	}
	// An empty allowedPrompts asks for nothing.
	d := translatePayload(t, "claude", planPayload(map[string]any{"plan": testPlan, "allowedPrompts": []any{}}, nil))
	if d.Approval == nil {
		t.Error("an empty allowedPrompts kept the plan from the Inbox")
	}
}

func TestApprovalNamesToolTargetAndDenyMessage(t *testing.T) {
	d := translatePayload(t, "claude", map[string]any{
		"hook_event_name": "PermissionRequest", "tool_name": "Bash",
		"tool_input": map[string]any{"command": "rm -rf build/", "description": "clean"},
	})
	if a := d.Approval; a == nil || a.Kind != KindApproval || a.Tool != "Bash" || a.Target != "rm -rf build/" || !a.DenyMessage {
		t.Errorf("claude approval %+v", d.Approval)
	}
	for _, harness := range []string{"opencode", "kilo"} {
		d := translatePayload(t, harness, map[string]any{
			"hook_event_name": "permission.asked", "session_id": "s", "permission_id": "per_1",
			"tool": "bash", "tool_input": map[string]any{"command": "git push -f"},
		})
		if a := d.Approval; a == nil || a.Tool != "bash" || a.Target != "git push -f" || !a.DenyMessage {
			t.Errorf("%s approval %+v", harness, d.Approval)
		}
	}
}

func TestPlanTitle(t *testing.T) {
	for in, want := range map[string]string{
		"# Refactor\nbody":       "Refactor",
		"\n\n  ## Two words  \n": "Two words",
		"":                       "a plan",
		"###":                    "a plan",
		strings.Repeat("y", 300): strings.Repeat("y", MaxMessage-3) + "...",
	} {
		if got := PlanTitle(in); got != want {
			t.Errorf("PlanTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
