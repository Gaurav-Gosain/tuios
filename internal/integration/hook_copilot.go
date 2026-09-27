package integration

// The GitHub Copilot CLI event map. Source: the hooks reference
// (https://docs.github.com/en/copilot/reference/hooks-reference, the
// content/copilot/reference/hooks-reference.md page of github/docs), read for
// this change. The hook file is tuios's own ~/.copilot/hooks/tuios.json.
//
// Events registered with their PascalCase names get payloads in the VS Code
// shape: hook_event_name and session_id in snake case. The notification event
// has no PascalCase name; its payload says hook_event_name "Notification" and
// sessionId. So the session id is read under both spellings.
//
//	SessionStart                      idle, and the session id
//	UserPromptSubmit                  working
//	PreToolUse                        working, except AskUserQuestion (the
//	                                  runtime's ask_user), which is
//	                                  needs_input, kind question
//	PostToolUse, PostToolUseFailure   working, only if the pane is in
//	                                  needs_input
//	Notification permission_prompt    needs_input, kind approval
//	Notification elicitation_dialog   needs_input, kind question
//	Stop                              done
//	ErrorOccurred, not recoverable    errored, with the error's message
//	SessionEnd                        none
//	subagent events and background notifications: nothing
//
// Copilot's permissionRequest hook is not used, for the state or for the
// Inbox. The reference says it "fires before the permission service runs",
// before the rules, the session's approvals and auto-allow, so it fires for
// calls Copilot then allows without asking. A pane would read as blocked on
// every tool call, and a hook that held the call for the Inbox would ask the
// person about calls nobody needed to see. The permission_prompt notification
// "only fires when a prompt is actually shown", so it is the blocked signal.
// It is fire-and-forget, so Copilot's prompts are answered in the pane.
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
			msg := Clip(input.first("question", "prompt", "message"))
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
			msg := Clip(p.str("message"))
			if msg == "" {
				msg = "approve a tool call"
			}
			return send(Copilot, event, id(Report{State: "needs_input", Kind: "approval", Message: msg}))
		case "elicitation_dialog":
			msg := Clip(p.str("message"))
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
		if m := Clip(p.obj("error").str("message")); m != "" {
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
