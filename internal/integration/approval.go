package integration

import (
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/invisible"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/oneline"
)

// Decisions a held prompt can end with, the same words the daemon uses.
const (
	DecisionOnce   = "once"
	DecisionAlways = "always"
	DecisionDeny   = "deny"
)

// ApprovalHookTimeout is the limit, in seconds, the Claude Code integration
// gives its PermissionRequest hook: past the daemon's longest hold of 300
// seconds, so the daemon always ends a hold before the harness kills the hook.
const ApprovalHookTimeout = 310

// DefaultDenyMessage is what the model is told when the person denies a call
// and gave no reason.
const DefaultDenyMessage = "The user denied this from the tuios Inbox."

// MaxScopeLines bounds the rules "always" may add. More than this is not
// offered: the Inbox shows every rule beside the key, and a long list is not
// something a person reads before pressing it.
const MaxScopeLines = 4

// Kinds of held prompt, the words request-approval takes.
const (
	// KindApproval is a tool call to allow or deny.
	KindApproval = "approval"
	// KindPlan is a plan an agent in plan mode asks to have approved before
	// it starts editing.
	KindPlan = "plan"
)

// MaxPlan bounds the plan text a hook hands the Inbox, in bytes. A longer
// plan is answered in the pane.
const MaxPlan = 32 << 10

// PlanSummaryPrefix starts the message a plan prompt reports, before the
// plan's title, so every surface that shows the line says what it is.
const PlanSummaryPrefix = "plan: "

// AcceptEditsScope is the one line "always" shows for a plan: the only mode
// change the Inbox offers with a plan.
const AcceptEditsScope = "Mode accept edits, for this session"

// DefaultPlanDenyMessage is what the model is told when the person keeps it
// planning and gave no reason.
const DefaultPlanDenyMessage = "The user wants you to keep planning. Revise the plan before you start editing."

// Approval is a prompt the harness lets a hook answer.
type Approval struct {
	// Kind is KindApproval or KindPlan. Empty reads as KindApproval.
	Kind string `json:"kind,omitempty"`
	// Options are the decisions the harness can take for this prompt.
	Options []string `json:"options"`
	// Scope is what "always" allows from now on, one rule per line, as the
	// Inbox shows it beside the key. It is set exactly when Options holds
	// always, and it is the whole of what the answer adds.
	Scope []string `json:"scope,omitempty"`
	// Tool and Target name the call for the risk rules: the tool, and the
	// argument that decides what it does (the command, the path). Empty for a
	// plan.
	Tool   string `json:"tool,omitempty"`
	Target string `json:"target,omitempty"`
	// Cwd is the folder the call runs in, as the harness sent it (the hook
	// payload's cwd). The risk rules resolve a relative Target against it.
	// Empty when the harness did not send one.
	Cwd string `json:"cwd,omitempty"`
	// Plan is a plan's text, whole. Set only for KindPlan.
	Plan string `json:"plan,omitempty"`
	// DenyMessage says the harness hands the model a reason with a deny, so
	// the Inbox may offer to type one.
	DenyMessage bool `json:"deny_message,omitempty"`
	suggestions json.RawMessage
	input       json.RawMessage
}

// IsPlan reports whether the prompt is a plan.
func (a *Approval) IsPlan() bool {
	return a != nil && a.Kind == KindPlan
}

// Offers reports whether the prompt takes decision.
func (a *Approval) Offers(decision string) bool {
	return a != nil && slices.Contains(a.Options, decision)
}

// Answer is what the hook prints for a decision, and false when it must print
// nothing: no approval, a decision the prompt was not offered, or a harness
// with no answer format.
func (a *Approval) Answer(harness, decision, message string) (string, bool) {
	if !a.Offers(decision) {
		return "", false
	}
	id, _ := Canonical(harness)
	switch id {
	case ClaudeCode:
		if a.IsPlan() {
			return claudePlanAnswer(decision, message, a.input, len(a.suggestions) > 0)
		}
		return claudeAnswer(decision, message, a.suggestions)
	case OpenCode, Kilo:
		return openCodeAnswer(decision, message)
	case Qwen:
		return qwenAnswer(decision, message)
	}
	return "", false
}

func claudeAnswer(decision, message string, suggestions json.RawMessage) (string, bool) {
	inner := map[string]any{}
	switch decision {
	case DecisionOnce:
		inner["behavior"] = "allow"
	case DecisionAlways:
		if len(suggestions) == 0 {
			return "", false
		}
		inner["behavior"] = "allow"
		inner["updatedPermissions"] = suggestions
	case DecisionDeny:
		inner["behavior"] = "deny"
		if strings.TrimSpace(message) == "" {
			message = DefaultDenyMessage
		}
		inner["message"] = message
	default:
		return "", false
	}
	return permissionRequestOutput(inner)
}

func permissionRequestOutput(inner map[string]any) (string, bool) {
	out, err := jsonx.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName": "PermissionRequest",
			"decision":      inner,
		},
	})
	if err != nil {
		return "", false
	}
	return string(out) + "\n", true
}

func claudePlanAnswer(decision, message string, input json.RawMessage, acceptEdits bool) (string, bool) {
	inner := map[string]any{}
	switch decision {
	case DecisionOnce, DecisionAlways:
		if len(input) == 0 {
			return "", false
		}
		inner["behavior"] = "allow"
		inner["updatedInput"] = input
		if decision == DecisionAlways {
			if !acceptEdits {
				return "", false
			}
			inner["updatedPermissions"] = []map[string]any{{"type": "setMode", "mode": "acceptEdits", "destination": "session"}}
		}
	case DecisionDeny:
		inner["behavior"] = "deny"
		if strings.TrimSpace(message) == "" {
			message = DefaultPlanDenyMessage
		}
		inner["message"] = message
	default:
		return "", false
	}
	return permissionRequestOutput(inner)
}

func openCodeAnswer(decision, message string) (string, bool) {
	reply := map[string]string{}
	switch decision {
	case DecisionOnce, DecisionAlways:
		reply["reply"] = decision
	case DecisionDeny:
		reply["reply"] = "reject"
		if strings.TrimSpace(message) == "" {
			message = DefaultDenyMessage
		}
		reply["message"] = message
	default:
		return "", false
	}
	out, err := jsonx.Marshal(reply)
	if err != nil {
		return "", false
	}
	return string(out) + "\n", true
}

type toolShape struct {
	key    string
	others []string
}

var claudeWholeTools = map[string]toolShape{
	"Bash":      {key: "command", others: []string{"description", "timeout", "run_in_background"}},
	"Read":      {key: "file_path", others: []string{"offset", "limit", "pages"}},
	"Glob":      {key: "pattern"},
	"Grep":      {key: "pattern", others: []string{"glob", "type", "output_mode", "-i", "-n", "-A", "-B", "-C", "context", "head_limit", "offset", "multiline"}},
	"WebFetch":  {key: "url", others: []string{"prompt"}},
	"WebSearch": {key: "query", others: []string{"allowed_domains", "blocked_domains"}},
}

var openCodeWholeTools = map[string]toolShape{
	"bash":     {key: "command", others: []string{"description", "timeout"}},
	"read":     {key: "filePath", others: []string{"offset", "limit"}},
	"webfetch": {key: "url", others: []string{"format", "timeout"}},
}

func shownWhole(shapes map[string]toolShape, tool string, input fields) bool {
	shape, ok := shapes[tool]
	if !ok {
		return false
	}
	value, ok := input[shape.key].(string)
	if !ok || value == "" {
		return false
	}
	for k := range input {
		if k != shape.key && !slices.Contains(shape.others, k) {
			return false
		}
	}
	full := tool + ": " + value
	return ToolSummary(tool, input) == full && invisible.Printable(full)
}

var claudeHeldTools = []string{"AskUserQuestion"}

const claudePlanTool = "ExitPlanMode"

func claudeApproval(p fields) *Approval {
	tool := p.str("tool_name")
	if tool == claudePlanTool {
		return claudePlanApproval(p)
	}
	if slices.Contains(claudeHeldTools, tool) {
		return nil
	}
	input := p.obj("tool_input")
	if !shownWhole(claudeWholeTools, tool, input) {
		return nil
	}
	a := &Approval{
		Kind:        KindApproval,
		Options:     []string{DecisionOnce, DecisionDeny},
		Tool:        tool,
		Target:      input.str(claudeWholeTools[tool].key),
		Cwd:         p.str("cwd"),
		DenyMessage: true,
	}
	if scope, rules, ok := claudeScope(p["permission_suggestions"]); ok {
		a.Scope, a.suggestions = scope, rules
		a.Options = []string{DecisionOnce, DecisionAlways, DecisionDeny}
	}
	return a
}

var claudePlanKeys = []string{"plan", "planFilePath"}

func claudePlanApproval(p fields) *Approval {
	input := p.obj("tool_input")
	plan := input.str("plan")
	if strings.TrimSpace(plan) == "" || len(plan) > MaxPlan || !utf8.ValidString(plan) {
		return nil
	}
	for k, v := range input {
		if slices.Contains(claudePlanKeys, k) {
			if _, ok := v.(string); !ok {
				return nil
			}
			continue
		}
		if list, ok := v.([]any); k == "allowedPrompts" && ok && len(list) == 0 {
			continue
		}
		return nil
	}
	raw, err := jsonx.Marshal(map[string]any(input))
	if err != nil {
		return nil
	}
	a := &Approval{
		Kind:        KindPlan,
		Options:     []string{DecisionOnce, DecisionDeny},
		Plan:        plan,
		DenyMessage: true,
		input:       raw,
	}
	if claudeAcceptEdits(p["permission_suggestions"]) {
		a.Scope = []string{AcceptEditsScope}
		a.Options = []string{DecisionOnce, DecisionAlways, DecisionDeny}
		a.suggestions = json.RawMessage(`[{"type":"setMode","mode":"acceptEdits","destination":"session"}]`)
	}
	return a
}

func claudeAcceptEdits(raw any) bool {
	list, ok := raw.([]any)
	if !ok || len(list) != 1 {
		return false
	}
	s, ok := list[0].(map[string]any)
	return ok && onlyKeys(s, "type", "mode", "destination") &&
		s["type"] == "setMode" && s["mode"] == "acceptEdits" && s["destination"] == "session"
}

// PlanTitle is the line a plan is known by: its first line that is not blank,
// without heading marks, clipped to a message.
func PlanTitle(plan string) string {
	for line := range strings.SplitSeq(plan, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if line != "" {
			return oneline.Clip(line)
		}
	}
	return "a plan"
}

var claudeDestinations = map[string]string{
	"session":         "for this session",
	"localSettings":   "in .claude/settings.local.json",
	"projectSettings": "in .claude/settings.json",
	"userSettings":    "in ~/.claude/settings.json",
}

func claudeScope(raw any) ([]string, json.RawMessage, bool) {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, nil, false
	}
	var scope []string
	var rebuilt []map[string]any
	for _, entry := range list {
		s, ok := entry.(map[string]any)
		if !ok || !onlyKeys(s, "type", "rules", "behavior", "destination") {
			return nil, nil, false
		}
		dest, _ := s["destination"].(string)
		where, known := claudeDestinations[dest]
		if s["type"] != "addRules" || s["behavior"] != "allow" || !known {
			return nil, nil, false
		}
		rules, ok := s["rules"].([]any)
		if !ok || len(rules) == 0 {
			return nil, nil, false
		}
		var out []map[string]any
		for _, r := range rules {
			rule, ok := r.(map[string]any)
			if !ok || !onlyKeys(rule, "toolName", "ruleContent") {
				return nil, nil, false
			}
			tool, _ := rule["toolName"].(string)
			content, isString := rule["ruleContent"].(string)
			if tool == "" || (rule["ruleContent"] != nil && !isString) {
				return nil, nil, false
			}
			line := tool + " (every call)"
			kept := map[string]any{"toolName": tool}
			if content != "" {
				line = tool + "(" + content + ")"
				kept["ruleContent"] = content
			}
			scope = append(scope, line+" "+where)
			out = append(out, kept)
		}
		rebuilt = append(rebuilt, map[string]any{"type": "addRules", "rules": out, "behavior": "allow", "destination": dest})
	}
	if !scopeShowable(scope) {
		return nil, nil, false
	}
	data, err := jsonx.Marshal(rebuilt)
	if err != nil {
		return nil, nil, false
	}
	return scope, data, true
}

func scopeShowable(scope []string) bool {
	if len(scope) == 0 || len(scope) > MaxScopeLines {
		return false
	}
	for _, line := range scope {
		if utf8.RuneCountInString(line) > oneline.MaxMessage || !invisible.Printable(line) || oneline.Clip(line) != line {
			return false
		}
	}
	return true
}

func onlyKeys(m map[string]any, keys ...string) bool {
	for k := range m {
		if !slices.Contains(keys, k) {
			return false
		}
	}
	return true
}

func openCodeApproval(p fields) *Approval {
	if p.str("permission_id") == "" {
		return nil
	}
	tool, input := p.str("tool"), p.obj("tool_input")
	if !shownWhole(openCodeWholeTools, tool, input) {
		return nil
	}
	a := &Approval{
		Kind:        KindApproval,
		Options:     []string{DecisionOnce, DecisionDeny},
		Tool:        tool,
		Target:      input.str(openCodeWholeTools[tool].key),
		DenyMessage: true,
	}
	permission := p.str("permission")
	if raw, ok := p["always"].([]any); ok && len(raw) > 0 && permission != "" {
		var scope []string
		for _, v := range raw {
			pattern, ok := v.(string)
			if !ok || pattern == "" {
				scope = nil
				break
			}
			scope = append(scope, permission+" "+pattern)
		}
		if scopeShowable(scope) {
			a.Scope = scope
			a.Options = []string{DecisionOnce, DecisionAlways, DecisionDeny}
		}
	}
	return a
}
