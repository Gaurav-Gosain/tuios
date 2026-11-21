package integration

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/oneline"
)

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
		if d.Approval != nil {
			d.Approval.Cwd = p.str("cwd")
		}
		return d
	case "PostToolUse", "PostToolUseFailure", "PermissionDenied":
		return send(Qwen, event, identity(Report{State: "working", IfState: claudeClearsBlock}, p))
	case "Notification":
		switch p.str("notification_type") {
		case "permission_prompt":
			return send(Qwen, event, identity(Report{State: "needs_input", Kind: "approval", Message: oneline.Clip(p.str("message"))}, p))
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
			msg = "stopped on " + oneline.Clip(t)
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

const qwenQuestionTool = "ask_user_question"

func qwenQuestion(input fields) string {
	if list, ok := input["questions"].([]any); ok && len(list) > 0 {
		if q, ok := list[0].(map[string]any); ok {
			if text := oneline.Clip(fields(q).first("question", "header")); text != "" {
				return text
			}
		}
	}
	return "a question"
}

var qwenWholeTools = map[string]toolShape{
	"run_shell_command": {key: "command", others: []string{"description", "timeout", "is_background"}},
	"read_file":         {key: "file_path", others: []string{"offset", "limit"}},
	"web_fetch":         {key: "url", others: []string{"prompt"}},
}

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
