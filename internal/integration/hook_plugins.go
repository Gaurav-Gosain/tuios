package integration

import "github.com/Gaurav-Gosain/tuios/internal/oneline"

func translateAmp(in Input, p fields) Decision {
	event := eventName(in, p)
	sid := p.str("session_id")
	switch event {
	case "session.start":
		if sid == "" {
			return skip(Amp, event, "the payload names no session")
		}
		return send(Amp, event, Report{SessionOnly: true, SessionID: sid})
	case "agent.start":
		return send(Amp, event, Report{State: "working", SessionID: sid})
	case "tool.call":
		if p.str("tool") != "ask_user_choice" {
			return skip(Amp, event, "tool "+p.str("tool")+" is not a question")
		}
		msg := oneline.Clip(p.str("question"))
		if msg == "" {
			msg = "a question"
		}
		return send(Amp, event, Report{State: "needs_input", Kind: "question", Message: msg, SessionID: sid})
	case "tool.result":
		if p.str("tool") != "ask_user_choice" {
			return skip(Amp, event, "tool "+p.str("tool")+" is not a question")
		}
		return send(Amp, event, Report{State: "working", IfState: claudeClearsBlock, SessionID: sid})
	case "agent.end":
		switch p.str("status") {
		case "done":
			return send(Amp, event, Report{State: "done", SessionID: sid})
		case "error":
			return send(Amp, event, Report{State: "errored", Message: "the turn ended on an error", SessionID: sid})
		case "cancelled":
			return send(Amp, event, Report{State: "idle", SessionID: sid})
		default:
			return skip(Amp, event, "agent.end status "+p.str("status")+" is not a state change")
		}
	case "":
		return skip(Amp, event, "the payload names no event")
	default:
		return skip(Amp, event, "event not mapped")
	}
}

func translateKimi(in Input, p fields) Decision {
	event := eventName(in, p)
	id := func(r Report) Report {
		r.SessionID = p.str("session_id")
		return r
	}
	switch event {
	case "SessionStart":
		return send(Kimi, event, id(Report{State: "idle"}))
	case "UserPromptSubmit":
		return send(Kimi, event, id(Report{State: "working"}))
	case "PreToolUse":
		if p.str("tool_name") == "AskUserQuestion" {
			msg := oneline.Clip(p.obj("tool_input").first("question", "prompt"))
			return send(Kimi, event, id(Report{State: "needs_input", Kind: "question", Message: msg}))
		}
		return send(Kimi, event, id(Report{State: "working"}))
	case "PermissionRequest":
		msg := "approve " + ToolSummary(p.str("tool_name"), p.obj("tool_input"))
		return send(Kimi, event, id(Report{State: "needs_input", Kind: "approval", Message: msg}))
	case "PostToolUse", "PostToolUseFailure", "PermissionResult":
		return send(Kimi, event, id(Report{State: "working", IfState: claudeClearsBlock}))
	case "Stop":
		return send(Kimi, event, id(Report{State: "done"}))
	case "StopFailure":
		msg := "stopped on an error"
		if t := p.first("error_type", "error"); t != "" {
			msg = "stopped on " + oneline.Clip(t)
		}
		return send(Kimi, event, id(Report{State: "errored", Message: msg}))
	case "Interrupt":
		return send(Kimi, event, id(Report{State: "idle"}))
	case "SessionEnd":
		return send(Kimi, event, id(Report{State: "none"}))
	case "":
		return skip(Kimi, event, "the payload names no event")
	default:
		return skip(Kimi, event, "event not mapped")
	}
}

func piTurnStart(id, event string, p fields) (Decision, bool) {
	r := Report{SessionID: p.str("session_id")}
	switch event {
	case "session_start":
		r.State = "idle"
		if busy, _ := p["busy"].(bool); busy {
			r.State = "working"
		}
		r.TranscriptPath = p.str("transcript_path")
	case "agent_start":
		r.State = "working"
	default:
		return Decision{}, false
	}
	return send(id, event, r), true
}

func translatePi(in Input, p fields) Decision {
	event := eventName(in, p)
	var leaf *string
	if s, ok := p["transcript_leaf"].(string); ok {
		leaf = &s
	}
	if d, ok := piTurnStart(Pi, event, p); ok {
		d.Report.TranscriptLeaf = leaf
		d.Report.TranscriptPath = p.str("transcript_path")
		return d
	}
	r := Report{SessionID: p.str("session_id"), TranscriptPath: p.str("transcript_path"), TranscriptLeaf: leaf}
	switch event {
	case "transcript_update":
		if r.SessionID == "" || r.TranscriptPath == "" || leaf == nil {
			return skip(Pi, event, "the transcript update needs a session, file and selected leaf")
		}
		r.SessionOnly = true
	case "agent_settled":
		r.State = "done"
	case "ui_prompt_start":
		r.State, r.Kind = "needs_input", "question"
		if p.str("kind") == "confirm" {
			r.Kind = "approval"
		}
		r.Message = oneline.Clip(p.str("title"))
		if r.Message == "" {
			r.Message = "waits for an answer"
		}
	case "ui_prompt_end":
		r.State, r.IfState = "idle", claudeClearsBlock
		if busy, _ := p["busy"].(bool); busy {
			r.State = "working"
		}
	case "subagent_start", "subagent_stop", "subagent_update":
		id := p.str("agent_id")
		if !ValidSubagentID(id) {
			return skip(Pi, event, "agent_id is not an id tuios keeps: 1 to 128 letters, digits, '_', '.', ':', '@' or '-'")
		}
		a := &Activity{Event: event, AgentID: id, AgentType: activityText(p.str("agent_type")), Text: activityText(p.str("text"))}
		if event == "subagent_update" {
			a.Tool, a.Target = p.str("tool"), oneline.Clip(p.str("target"))
			switch p.str("outcome") {
			case "stopping":
				a.Outcome = OutcomeStopping
			case "failed":
				a.Outcome = OutcomeFailed
			}
		}
		return send(Pi, event, Report{SessionID: p.str("session_id"), Activity: a})
	case "":
		return skip(Pi, event, "the payload names no event")
	default:
		return skip(Pi, event, "event not mapped")
	}
	return send(Pi, event, r)
}

func translateOMP(in Input, p fields) Decision {
	event := eventName(in, p)
	if d, ok := piTurnStart(OMP, event, p); ok {
		return d
	}
	r := Report{SessionID: p.str("session_id")}
	switch event {
	case "agent_end":
		r.State = "done"
	case "tool_approval_requested":
		r.State, r.Kind = "needs_input", "approval"
		r.Message = oneline.Clip(p.str("reason"))
		if r.Message == "" {
			r.Message = "approve a tool call"
			if tool := oneline.Clip(p.str("tool_name")); tool != "" {
				r.Message = "approve " + tool
			}
		}
	case "tool_approval_resolved":
		r.State, r.IfState = "working", claudeClearsBlock
	case "":
		return skip(OMP, event, "the payload names no event")
	default:
		return skip(OMP, event, "event not mapped")
	}
	return send(OMP, event, r)
}

func translateVibe(in Input, p fields) Decision {
	event := eventName(in, p)
	if p.str("parent_session_id") != "" {
		return skip(Vibe, event, "subagent events do not drive the parent pane's state")
	}
	switch event {
	case "session_start":
		r := identity(Report{State: "idle", SessionStart: true}, p)
		r.Activity = &Activity{Event: ActivitySessionStart, Text: activityText(p.str("source")), Model: activityText(p.str("model"))}
		return send(Vibe, event, r)
	case "pre_agent":
		r := identity(Report{State: "working"}, p)
		r.Activity = &Activity{Event: ActivityPrompt, Text: activityText(p.str("prompt"))}
		return send(Vibe, event, r)
	case "pre_tool":
		tool := p.str("tool_name")
		input := p.obj("tool_input")
		if tool == "ask_user_question" || tool == "AskUserQuestion" {
			msg := oneline.Clip(input.first("question", "prompt"))
			return send(Vibe, event, identity(Report{State: "needs_input", Kind: "question", Message: msg}, p))
		}
		r := identity(Report{State: "working"}, p)
		r.Activity = &Activity{Event: ActivityTool, Tool: tool, Target: ToolTarget(tool, input)}
		return send(Vibe, event, r)
	case "post_tool":
		tool := p.str("tool_name")
		input := p.obj("tool_input")
		r := identity(Report{State: "working", IfState: claudeClearsBlock}, p)
		r.Activity = &Activity{Event: ActivityToolDone, Tool: tool, Target: ToolTarget(tool, input)}
		return send(Vibe, event, r)
	case "notification":
		kind := "question"
		if p.str("notification_type") == "permission_prompt" {
			kind = "approval"
		}
		title := oneline.Clip(p.str("title"))
		msg := oneline.Clip(p.str("message"))
		switch {
		case title != "" && msg != "":
			msg = title + ": " + msg
		case title != "":
			msg = title
		}
		return send(Vibe, event, identity(Report{State: "needs_input", Kind: kind, Message: msg}, p))
	case "post_agent":
		r := identity(Report{State: "done"}, p)
		r.Activity = &Activity{Event: ActivityTurnEnd, Text: activityText(p.first("response", "last_assistant_message"))}
		return send(Vibe, event, r)
	case "session_end":
		return send(Vibe, event, identity(Report{State: "none"}, p))
	case "":
		return skip(Vibe, event, "the payload names no event")
	default:
		return skip(Vibe, event, "event not mapped")
	}
}
