package integration

import (
	"encoding/json"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Answering a permission prompt from the Inbox.
//
// Some harnesses let a hook decide a permission prompt: Claude Code reads a
// decision from its PermissionRequest hook's stdout, and opencode takes a reply
// to a permission request through its SDK, which the tuios plugin sends with
// what the hook prints. For those events Translate adds an Approval to the
// report, and `tuios agent-hook` may hold the prompt with the daemon's
// request-approval verb until the person answers it in the Inbox.
//
// The person answers from one line of text, so a prompt is only offered to the
// Inbox when that line is the whole request (see shownWhole): a tool whose
// effect is decided by one argument, with that argument shown in full, nothing
// redacted, clipped or collapsed, and no other argument that changes what the
// call does. A Write or Edit, whose body the line cannot show, an MCP tool, a
// command too long for the line, or a call with an argument the line leaves
// out is answered in the pane as before. "Always" is only offered with the
// exact rules it adds, which the Inbox shows beside its key (Approval.Scope).
//
// Answer is the only place a decision becomes output, and it prints one only
// for a decision the harness was offered. Everything else prints nothing,
// which every harness here reads as "no opinion, ask the user". So an error, a
// timeout, a daemon that is gone or an answer the hook does not recognise can
// never approve anything.
//
// Codex is left out on purpose: its PermissionRequest hook runs before its own
// reviewer has decided whether to ask at all, so a hook that waited would ask
// the person about calls Codex would have allowed or refused by itself.

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

// Approval is a prompt the harness lets a hook answer.
type Approval struct {
	// Options are the decisions the harness can take for this prompt.
	Options []string `json:"options"`
	// Scope is what "always" allows from now on, one rule per line, as the
	// Inbox shows it beside the key. It is set exactly when Options holds
	// always, and it is the whole of what the answer adds.
	Scope []string `json:"scope,omitempty"`
	// suggestions are the rules "always" hands back to Claude Code, rebuilt
	// from the fields Scope shows so nothing unshown rides along.
	suggestions json.RawMessage
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
		return claudeAnswer(decision, message, a.suggestions)
	case OpenCode, Kilo:
		return openCodeAnswer(decision, message)
	}
	return "", false
}

// claudeAnswer is Claude Code's PermissionRequest decision. The shape is the
// hooks reference's "PermissionRequest decision control": hookSpecificOutput
// with hookEventName and a decision whose behavior is allow or deny, with
// updatedPermissions to add rules on an allow and message on a deny.
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
	out, err := json.Marshal(map[string]any{
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

// openCodeAnswer is what the tuios opencode plugin reads: the reply the
// plugin posts to opencode's permission route, once, always or reject.
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
	out, err := json.Marshal(reply)
	if err != nil {
		return "", false
	}
	return string(out) + "\n", true
}

// toolShape is a tool whose call one line can show in full: the argument that
// decides what it does, and the other arguments that change nothing a person
// approves (a label, a timeout, a line range). An argument not named here
// keeps the call out of the Inbox.
type toolShape struct {
	key    string
	others []string
}

// claudeWholeTools are the Claude Code tools the Inbox may answer. Write, Edit,
// MultiEdit and NotebookEdit are not here because their body is what matters
// and the line shows only the path. Bash's dangerouslyDisableSandbox is not an
// ignorable argument, so a call that sets it is answered in the pane. Glob and
// Grep are only here without a path, since the line shows one argument.
var claudeWholeTools = map[string]toolShape{
	"Bash":      {key: "command", others: []string{"description", "timeout", "run_in_background"}},
	"Read":      {key: "file_path", others: []string{"offset", "limit", "pages"}},
	"Glob":      {key: "pattern"},
	"Grep":      {key: "pattern", others: []string{"glob", "type", "output_mode", "-i", "-n", "-A", "-B", "-C", "context", "head_limit", "offset", "multiline"}},
	"WebFetch":  {key: "url", others: []string{"prompt"}},
	"WebSearch": {key: "query", others: []string{"allowed_domains", "blocked_domains"}},
}

// openCodeWholeTools are the opencode tools the Inbox may answer, by the tool
// name and arguments the plugin sees in tool.execute.before. bash's workdir is
// not ignorable: where a command runs is part of what it does.
var openCodeWholeTools = map[string]toolShape{
	"bash":     {key: "command", others: []string{"description", "timeout"}},
	"read":     {key: "filePath", others: []string{"offset", "limit"}},
	"webfetch": {key: "url", others: []string{"format", "timeout"}},
}

// shownWhole reports whether ToolSummary(tool, input) is the whole call: the
// tool is one whose call one argument decides, that argument is the one the
// summary shows, unchanged (not clipped, redacted or with its whitespace
// collapsed) and printable, and every other argument is one that changes
// nothing a person approves.
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
	return ToolSummary(tool, input) == full && printableLine(full)
}

// printableLine reports whether s reads on screen as it is: every rune a
// visible character or a plain space. A format character such as a bidi
// override, which can make a line read differently from what it holds, fails.
func printableLine(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// claudeHeldTools are tools whose PermissionRequest is not a yes or no: an
// answer to a question or a plan choice rides on the decision's updatedInput,
// which the Inbox does not collect. They are left to Claude Code's own dialog.
var claudeHeldTools = []string{"AskUserQuestion", "ExitPlanMode"}

// claudeApproval is the Approval for a Claude Code PermissionRequest, or nil
// for a call the Inbox does not answer: a question or plan choice, or a call
// its line does not show whole.
func claudeApproval(p fields) *Approval {
	tool := p.str("tool_name")
	if slices.Contains(claudeHeldTools, tool) {
		return nil
	}
	if !shownWhole(claudeWholeTools, tool, p.obj("tool_input")) {
		return nil
	}
	a := &Approval{Options: []string{DecisionOnce, DecisionDeny}}
	if scope, rules, ok := claudeScope(p["permission_suggestions"]); ok {
		a.Scope, a.suggestions = scope, rules
		a.Options = []string{DecisionOnce, DecisionAlways, DecisionDeny}
	}
	return a
}

// claudeDestinations are where an added rule is kept, as the person reads it.
// A destination not listed here is not offered.
var claudeDestinations = map[string]string{
	"session":         "for this session",
	"localSettings":   "in .claude/settings.local.json",
	"projectSettings": "in .claude/settings.json",
	"userSettings":    "in ~/.claude/settings.json",
}

// claudeScope reads permission_suggestions into what "always" shows and what
// it hands back. It offers always only when every suggestion is an addRules
// that allows, with a destination it can name and rules with only a tool name
// and a rule content. A setMode (such as acceptEdits), an addDirectories, a
// deny or ask rule, or any field it does not know is not offered, since the
// Inbox could not show what it does. The rules handed back are rebuilt from
// the fields shown, so nothing the person did not see reaches Claude Code.
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
	data, err := json.Marshal(rebuilt)
	if err != nil {
		return nil, nil, false
	}
	return scope, data, true
}

// scopeShowable reports whether every line of a scope can be shown as it is:
// at most MaxScopeLines lines, each one printable and no longer than a message.
func scopeShowable(scope []string) bool {
	if len(scope) == 0 || len(scope) > MaxScopeLines {
		return false
	}
	for _, line := range scope {
		if utf8.RuneCountInString(line) > MaxMessage || !printableLine(line) || Clip(line) != line {
			return false
		}
	}
	return true
}

// onlyKeys reports whether m has no key outside keys.
func onlyKeys(m map[string]any, keys ...string) bool {
	for k := range m {
		if !slices.Contains(keys, k) {
			return false
		}
	}
	return true
}

// openCodeApproval is the Approval for an opencode permission.asked the plugin
// can answer: one that carries the permission id the reply goes to, and the
// tool call it is about, shown whole. "Always" is offered with the patterns
// opencode stops asking about, from the request's always list.
func openCodeApproval(p fields) *Approval {
	if p.str("permission_id") == "" {
		return nil
	}
	if !shownWhole(openCodeWholeTools, p.str("tool"), p.obj("tool_input")) {
		return nil
	}
	a := &Approval{Options: []string{DecisionOnce, DecisionDeny}}
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
