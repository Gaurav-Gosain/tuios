package integration

import "slices"

var identityEvents = map[string][]string{
	Devin:       {"SessionStart", "UserPromptSubmit"},
	Droid:       {"SessionStart"},
	Qoder:       {"SessionStart"},
	Grok:        {"SessionStart", "session_start", "sessionStart"},
	Antigravity: {"PreInvocation"},
	Crush:       {"PreToolUse", "pretooluse", "pre_tool_use"},
	Hermes:      {"on_session_start", "on_session_reset"},
}

var identitySessionKeys = map[string][]string{
	Antigravity: {"conversationId", "conversation_id"},
}

func translateIdentity(id string, in Input, p fields) Decision {
	event := eventName(in, p)
	events, ok := identityEvents[id]
	if !ok {
		return skip(id, event, "no hook mapping for harness "+id)
	}
	if event == "" && id == Antigravity {
		event = "PreInvocation"
	}
	if event == "" {
		return skip(id, event, "the payload names no event")
	}
	known := slices.Contains(events, event)
	if !known {
		return skip(id, event, "event not mapped")
	}
	if p.str("agent_id") != "" {
		return skip(id, event, "subagent event")
	}
	sid := ""
	switch id {
	case Grok:
		sid = in.env("GROK_SESSION_ID")
	case Crush:
		sid = in.env("CRUSH_SESSION_ID")
	}
	if sid == "" {
		keys := identitySessionKeys[id]
		if keys == nil {
			keys = []string{"session_id", "sessionId"}
		}
		sid = p.first(keys...)
	}
	if sid == "" {
		return skip(id, event, "the payload names no session")
	}
	return send(id, event, Report{SessionOnly: true, SessionID: sid})
}
