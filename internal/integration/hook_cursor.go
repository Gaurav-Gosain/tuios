package integration

// The Cursor Agent event map. Source: the hooks reference
// (https://cursor.com/docs/hooks, read for this change as its markdown,
// https://cursor.com/docs/hooks.md). Hooks are in ~/.cursor/hooks.json,
// version 1, each event a list of {command}. Every payload carries
// hook_event_name and conversation_id; sessionStart and sessionEnd carry
// session_id, which the reference says is the same as conversation_id.
//
//	sessionStart                      idle, and the session id
//	beforeSubmitPrompt                working
//	postToolUse, postToolUseFailure   working
//	stop completed                    done
//	stop aborted                      idle (the person stopped the turn)
//	stop error                        errored
//	sessionEnd                        none
//
// Only events Cursor treats as observations are registered. The reference
// calls preToolUse, beforeShellExecution, beforeMCPExecution, beforeReadFile
// and subagentStart permission hooks and blocks the action on output that is
// not a valid answer, so a hook that prints nothing there would block every
// call. None of them fires when Cursor shows its own approval prompt either,
// so they could not say the agent is waiting. That state, and answering the
// prompt, stay with the pane's screen rules, which may take over a working
// report while a prompt is on screen.
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
