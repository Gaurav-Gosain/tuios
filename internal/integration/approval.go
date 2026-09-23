package integration

import (
	"encoding/json"
	"slices"
	"strings"
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

// Approval is a prompt the harness lets a hook answer.
type Approval struct {
	// Options are the decisions the harness can take for this prompt.
	Options []string `json:"options"`
	// suggestions are Claude Code's permission_suggestions, the rules that
	// "always" hands back so the harness stops asking for calls like this one.
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

// claudeHeldTools are tools whose PermissionRequest is not a yes or no: an
// answer to a question or a plan choice rides on the decision's updatedInput,
// which the Inbox does not collect. They are left to Claude Code's own dialog.
var claudeHeldTools = []string{"AskUserQuestion", "ExitPlanMode"}

// claudeApproval is the Approval for a Claude Code PermissionRequest, or nil
// for a tool the Inbox does not answer.
func claudeApproval(p fields) *Approval {
	if slices.Contains(claudeHeldTools, p.str("tool_name")) {
		return nil
	}
	a := &Approval{Options: []string{DecisionOnce, DecisionDeny}}
	if raw, ok := p["permission_suggestions"].([]any); ok && len(raw) > 0 {
		if data, err := json.Marshal(raw); err == nil {
			a.suggestions = data
			a.Options = []string{DecisionOnce, DecisionAlways, DecisionDeny}
		}
	}
	return a
}

// openCodeApproval is the Approval for an opencode permission.asked the plugin
// can answer: one that carries the permission id the reply goes to.
func openCodeApproval(p fields) *Approval {
	if p.str("permission_id") == "" {
		return nil
	}
	return &Approval{Options: []string{DecisionOnce, DecisionAlways, DecisionDeny}}
}
