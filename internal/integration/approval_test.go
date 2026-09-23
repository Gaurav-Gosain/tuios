package integration

import (
	"encoding/json"
	"testing"
)

func translatePayload(t *testing.T, harness string, payload any) Decision {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return Translate(harness, Input{Payload: data, Getenv: func(string) string { return "" }})
}

// TestClaudePermissionRequestCanBeAnswered: PermissionRequest carries an
// Approval with the decisions Claude Code can take for it, and each decision
// becomes the hooks reference's decision object.
func TestClaudePermissionRequestCanBeAnswered(t *testing.T) {
	suggestions := []any{map[string]any{"type": "addRules", "rules": []any{map[string]any{"toolName": "Bash", "ruleContent": "go test:*"}}, "behavior": "allow", "destination": "localSettings"}}
	d := translatePayload(t, "claude", map[string]any{
		"hook_event_name":        "PermissionRequest",
		"session_id":             "s1",
		"tool_name":              "Bash",
		"tool_input":             map[string]any{"command": "go test ./..."},
		"permission_suggestions": suggestions,
	})
	if d.Report == nil || d.Report.State != "needs_input" || d.Approval == nil {
		t.Fatalf("decision %+v", d)
	}
	if got := d.Approval.Options; len(got) != 3 || got[0] != DecisionOnce || got[1] != DecisionAlways || got[2] != DecisionDeny {
		t.Fatalf("options %v", got)
	}

	cases := []struct {
		decision, message string
		want              string
	}{
		{DecisionOnce, "", `{"hookSpecificOutput":{"decision":{"behavior":"allow"},"hookEventName":"PermissionRequest"}}`},
		{DecisionAlways, "", `{"hookSpecificOutput":{"decision":{"behavior":"allow","updatedPermissions":[{"behavior":"allow","destination":"localSettings","rules":[{"ruleContent":"go test:*","toolName":"Bash"}],"type":"addRules"}]},"hookEventName":"PermissionRequest"}}`},
		{DecisionDeny, "", `{"hookSpecificOutput":{"decision":{"behavior":"deny","message":"` + DefaultDenyMessage + `"},"hookEventName":"PermissionRequest"}}`},
		{DecisionDeny, "not on main", `{"hookSpecificOutput":{"decision":{"behavior":"deny","message":"not on main"},"hookEventName":"PermissionRequest"}}`},
	}
	for _, tc := range cases {
		got, ok := d.Approval.Answer("claude-code", tc.decision, tc.message)
		if !ok || got != tc.want+"\n" {
			t.Errorf("%s answered %q %v\nwant %q", tc.decision, got, ok, tc.want)
		}
	}
}

// TestNothingIsPrintedForAnythingElse is the safety property: only a decision
// the prompt offered, for a harness with an answer format, prints anything.
func TestNothingIsPrintedForAnythingElse(t *testing.T) {
	plain := translatePayload(t, "claude", map[string]any{"hook_event_name": "PermissionRequest", "tool_name": "Edit"})
	if plain.Approval == nil {
		t.Fatal("a PermissionRequest with no suggestions has no Approval")
	}
	for _, tc := range []struct {
		name     string
		approval *Approval
		harness  string
		decision string
	}{
		{"always, not offered without suggestions", plain.Approval, "claude-code", DecisionAlways},
		{"an unknown decision", plain.Approval, "claude-code", "yes"},
		{"an empty decision", plain.Approval, "claude-code", ""},
		{"ask is not a decision", plain.Approval, "claude-code", "ask"},
		{"no approval at all", nil, "claude-code", DecisionOnce},
		{"a harness with no answer format", plain.Approval, "gemini-cli", DecisionOnce},
		{"a harness nobody knows", plain.Approval, "nope", DecisionOnce},
	} {
		if out, ok := tc.approval.Answer(tc.harness, tc.decision, ""); ok || out != "" {
			t.Errorf("%s printed %q", tc.name, out)
		}
	}
}

// TestOnlyYesNoPromptsAreOffered keeps a question, a plan choice and every
// other event to the harness's own dialog.
func TestOnlyYesNoPromptsAreOffered(t *testing.T) {
	for _, tool := range []string{"AskUserQuestion", "ExitPlanMode"} {
		d := translatePayload(t, "claude", map[string]any{"hook_event_name": "PermissionRequest", "tool_name": tool})
		if d.Report == nil || d.Approval != nil {
			t.Errorf("%s: %+v, want a report and no Approval", tool, d)
		}
	}
	for _, payload := range []map[string]any{
		{"hook_event_name": "Notification", "notification_type": "permission_prompt", "message": "Claude needs your permission"},
		{"hook_event_name": "PreToolUse", "tool_name": "Bash"},
	} {
		if d := translatePayload(t, "claude", payload); d.Approval != nil {
			t.Errorf("%v carries an Approval", payload)
		}
	}
	if d := translatePayload(t, "codex", map[string]any{"hook_event_name": "PermissionRequest", "tool_name": "Bash"}); d.Approval != nil {
		t.Error("Codex's PermissionRequest carries an Approval, but it runs before Codex's own reviewer")
	}
}

func TestOpenCodePermissionCanBeAnswered(t *testing.T) {
	for _, harness := range []string{"opencode", "kilo"} {
		d := translatePayload(t, harness, map[string]any{"hook_event_name": "permission.asked", "session_id": "s", "title": "bash", "permission_id": "per_1"})
		if d.Report == nil || d.Report.Kind != "approval" || d.Approval == nil {
			t.Fatalf("%s: %+v", harness, d)
		}
		for decision, want := range map[string]string{
			DecisionOnce:   `{"reply":"once"}`,
			DecisionAlways: `{"reply":"always"}`,
			DecisionDeny:   `{"message":"` + DefaultDenyMessage + `","reply":"reject"}`,
		} {
			if got, ok := d.Approval.Answer(harness, decision, ""); !ok || got != want+"\n" {
				t.Errorf("%s %s answered %q, want %q", harness, decision, got, want)
			}
		}
		// A request the plugin cannot reply to, an older plugin's, is not
		// held.
		old := translatePayload(t, harness, map[string]any{"hook_event_name": "permission.asked", "title": "bash"})
		if old.Report == nil || old.Approval != nil {
			t.Errorf("%s: a permission with no id: %+v", harness, old)
		}
	}
}

// TestClaudeHookOutlivesTheLongestHold keeps the installed limit above the
// daemon's cap, so the daemon always answers before Claude Code kills the hook.
func TestClaudeHookOutlivesTheLongestHold(t *testing.T) {
	tg := mustTarget(t, ClaudeCode)
	for _, ev := range tg.Events {
		if ev.Name == "PermissionRequest" && ev.Timeout != ApprovalHookTimeout {
			t.Errorf("PermissionRequest timeout %d, want %d", ev.Timeout, ApprovalHookTimeout)
		}
	}
	if ApprovalHookTimeout <= 300 {
		t.Errorf("the hook timeout %d does not outlive a 300 second hold", ApprovalHookTimeout)
	}
}
