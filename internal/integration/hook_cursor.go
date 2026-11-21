package integration

func translateCursor(in Input, p fields) Decision {
	event := eventName(in, p)
	id := func(r Report) Report {
		r.SessionID = p.first("session_id", "conversation_id", "sessionId", "conversationId")
		r.TranscriptPath = p.str("transcript_path")
		return r
	}
	switch event {
	case "sessionStart":
		if background, _ := p["is_background_agent"].(bool); background {
			return skip(CursorAgent, event, "a background agent runs in no pane")
		}
		return send(CursorAgent, event, id(Report{State: "idle"}))
	case "beforeSubmitPrompt":
		return send(CursorAgent, event, id(Report{State: "working"}))
	case "postToolUse", "postToolUseFailure":
		return send(CursorAgent, event, id(Report{State: "working"}))
	case "stop":
		switch p.str("status") {
		case "completed":
			return send(CursorAgent, event, id(Report{State: "done"}))
		case "aborted":
			return send(CursorAgent, event, id(Report{State: "idle"}))
		case "error":
			return send(CursorAgent, event, id(Report{State: "errored", Message: "the turn ended on an error"}))
		default:
			return skip(CursorAgent, event, "stop status "+p.str("status")+" is not a state change")
		}
	case "sessionEnd":
		return send(CursorAgent, event, id(Report{State: "none"}))
	case "subagentStart", "subagentStop":
		return skip(CursorAgent, event, "subagent event")
	case "":
		return skip(CursorAgent, event, "the payload names no event")
	default:
		return skip(CursorAgent, event, "event not mapped")
	}
}
