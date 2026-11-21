package integration

import "github.com/Gaurav-Gosain/tuios/internal/oneline"

func translateCopilot(in Input, p fields) Decision {
	event := eventName(in, p)
	if p.str("agent_id") != "" {
		return skip(Copilot, event, "subagent event")
	}
	id := func(r Report) Report {
		r.SessionID = p.first("session_id", "sessionId")
		r.TranscriptPath = p.first("transcript_path", "transcriptPath")
		return r
	}
	switch event {
	case "SessionStart", "sessionStart":
		return send(Copilot, event, id(Report{State: "idle"}))
	case "UserPromptSubmit", "userPromptSubmitted":
		return send(Copilot, event, id(Report{State: "working"}))
	case "PreToolUse", "preToolUse":
		if tool := p.first("tool_name", "toolName"); tool == "AskUserQuestion" || tool == "ask_user" {
			input := p.obj("tool_input")
			if len(input) == 0 {
				input = p.obj("toolArgs")
			}
			msg := oneline.Clip(input.first("question", "prompt", "message"))
			if msg == "" {
				msg = "a question"
			}
			return send(Copilot, event, id(Report{State: "needs_input", Kind: "question", Message: msg}))
		}
		return send(Copilot, event, id(Report{State: "working"}))
	case "PostToolUse", "PostToolUseFailure", "postToolUse", "postToolUseFailure":
		return send(Copilot, event, id(Report{State: "working", IfState: claudeClearsBlock}))
	case "Notification", "notification":
		switch p.str("notification_type") {
		case "permission_prompt":
			msg := oneline.Clip(p.str("message"))
			if msg == "" {
				msg = "approve a tool call"
			}
			return send(Copilot, event, id(Report{State: "needs_input", Kind: "approval", Message: msg}))
		case "elicitation_dialog":
			msg := oneline.Clip(p.str("message"))
			if msg == "" {
				msg = "a question"
			}
			return send(Copilot, event, id(Report{State: "needs_input", Kind: "question", Message: msg}))
		default:
			return skip(Copilot, event, "notification type "+p.str("notification_type")+" is not a state change")
		}
	case "Stop", "agentStop":
		return send(Copilot, event, id(Report{State: "done"}))
	case "ErrorOccurred", "errorOccurred":
		if recoverable, ok := p["recoverable"].(bool); !ok || recoverable {
			return skip(Copilot, event, "a recoverable error does not end the turn")
		}
		msg := "stopped on an error"
		if m := oneline.Clip(p.obj("error").str("message")); m != "" {
			msg = m
		}
		return send(Copilot, event, id(Report{State: "errored", Message: msg}))
	case "SessionEnd", "sessionEnd":
		return send(Copilot, event, id(Report{State: "none"}))
	case "":
		return skip(Copilot, event, "the payload names no event")
	default:
		return skip(Copilot, event, "event not mapped")
	}
}
