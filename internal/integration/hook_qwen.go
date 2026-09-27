package integration

import "strings"

// The Qwen Code event map. Source: the hooks reference in the Qwen Code
// repository (docs/users/features/hooks.md), and the code that runs the
// hooks (packages/core/src/core/toolHookTriggers.ts and coreToolScheduler.ts),
// read for this change. Hooks are in ~/.qwen/settings.json in Claude Code's
// nested shape. Every payload carries hook_event_name and session_id, and
// agent_id inside a subagent. The tool events carry the runtime tool id
// (run_shell_command, read_file, web_fetch, ask_user_question) in tool_name.
//
//	SessionStart                    idle, and the session id and transcript
//	                                path (source compact is mid-turn and
//	                                reports nothing)
//	UserPromptSubmit                working
//	PreToolUse                      working
//	PermissionRequest               needs_input, kind approval, or kind
//	                                question for ask_user_question
//	PostToolUse, PostToolUseFailure,
//	PermissionDenied                working, only if the pane is in needs_input
//	Notification permission_prompt needs_input, kind approval
//	Notification idle_prompt        idle, only if the pane is working or unknown
//	Stop                            done, with the last assistant message
//	StopFailure                     errored, with the error type
//	SessionEnd                      none
//	subagent events and any event with agent_id: nothing
//
// PermissionRequest runs after Qwen Code's own rules and auto-approval, just
// before it shows its permission dialog, and reads a decision back from the
// hook's stdout in Claude Code's shape (hookSpecificOutput.decision.behavior
// allow or deny, with a message on a deny). So the Inbox may answer it. Qwen
// Code ignores updatedPermissions on this path, so "always" is not offered.
// ask_user_question also comes through PermissionRequest, since it needs the
// person, and it is reported as a question that is answered in the pane.
func translateQwen(in Input, p fields) Decision {
	event := eventName(in, p)
	if p.str("agent_id") != "" {
		return skip(Qwen, event, "subagent event")
	}
	switch event {
	case "SessionStart":
		if p.str("source") == "compact" {
			return skip(Qwen, event, "compaction restarts the session mid-turn")
		}
		return send(Qwen, event, identity(Report{State: "idle"}, p))
	case "UserPromptSubmit":
		return send(Qwen, event, identity(Report{State: "working"}, p))
	case "PreToolUse":
		return send(Qwen, event, identity(Report{State: "working"}, p))
	case "PermissionRequest":
		tool, input := p.str("tool_name"), p.obj("tool_input")
		if tool == qwenQuestionTool {
			return send(Qwen, event, identity(Report{State: "needs_input", Kind: "question", Message: qwenQuestion(input)}, p))
		}
		d := send(Qwen, event, identity(Report{State: "needs_input", Kind: "approval", Message: "approve " + ToolSummary(tool, input)}, p))
		d.Approval = qwenApproval(tool, input)
		return d
	case "PostToolUse", "PostToolUseFailure", "PermissionDenied":
		return send(Qwen, event, identity(Report{State: "working", IfState: claudeClearsBlock}, p))
	case "Notification":
		switch p.str("notification_type") {
		case "permission_prompt":
			return send(Qwen, event, identity(Report{State: "needs_input", Kind: "approval", Message: Clip(p.str("message"))}, p))
		case "idle_prompt":
			return send(Qwen, event, identity(Report{State: "idle", IfState: "working,unknown"}, p))
		default:
			return skip(Qwen, event, "notification type "+p.str("notification_type")+" is not a state change")
		}
	case "Stop":
		return send(Qwen, event, turnEnd(identity(Report{State: "done"}, p), p))
	case "StopFailure":
		msg := "stopped on an error"
		if t := p.first("error", "error_type"); t != "" {
			msg = "stopped on " + Clip(t)
		}
		return send(Qwen, event, identity(Report{State: "errored", Message: msg}, p))
	case "SessionEnd":
		return send(Qwen, event, identity(Report{State: "none"}, p))
	case "SubagentStart", "SubagentStop":
		return skip(Qwen, event, "subagent event")
	case "":
		return skip(Qwen, event, "the payload names no event")
	default:
		return skip(Qwen, event, "event not mapped")
	}
}

// qwenQuestionTool is Qwen Code's tool for asking the person.
const qwenQuestionTool = "ask_user_question"

// qwenQuestion is the first question an ask_user_question call asks.
func qwenQuestion(input fields) string {
	if list, ok := input["questions"].([]any); ok && len(list) > 0 {
		if q, ok := list[0].(map[string]any); ok {
			if text := Clip(fields(q).first("question", "header")); text != "" {
				return text
			}
		}
	}
	return "a question"
}

// qwenWholeTools are the Qwen Code tools the Inbox may answer, by runtime id
// and the parameters the tools declare (packages/core/src/tools). The shell's
// directory is not ignorable: where a command runs is part of what it does.
var qwenWholeTools = map[string]toolShape{
	"run_shell_command": {key: "command", others: []string{"description", "timeout", "is_background"}},
	"read_file":         {key: "file_path", others: []string{"offset", "limit"}},
	"web_fetch":         {key: "url", others: []string{"prompt"}},
}

// qwenApproval is the Approval for a Qwen Code PermissionRequest the Inbox can
// show whole: allow once or deny.
func qwenApproval(tool string, input fields) *Approval {
	if !shownWhole(qwenWholeTools, tool, input) {
		return nil
	}
	return &Approval{
		Kind:        KindApproval,
		Options:     []string{DecisionOnce, DecisionDeny},
		Tool:        tool,
		Target:      input.str(qwenWholeTools[tool].key),
		DenyMessage: true,
	}
}

// qwenAnswer is Qwen Code's PermissionRequest decision: Claude Code's shape,
// with no permission updates.
func qwenAnswer(decision, message string) (string, bool) {
	switch decision {
	case DecisionOnce:
		return permissionRequestOutput(map[string]any{"behavior": "allow"})
	case DecisionDeny:
		if strings.TrimSpace(message) == "" {
			message = DefaultDenyMessage
		}
		return permissionRequestOutput(map[string]any{"behavior": "deny", "message": message})
	}
	return "", false
}
