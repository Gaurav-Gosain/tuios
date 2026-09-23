package integration

// The session-identity maps. These harnesses have a hook surface that can name
// the conversation running in the pane, and nothing more that tuios trusts:
// their hooks do not cover every lifecycle transition (an interrupt, a
// cancelled approval, the end of a turn), and a state a hook reports outranks
// every screen rule, so one missed event would hold the pane on working until
// the harness exits. So each reports the conversation id alone, through
// set-agent-session, and the pane's state keeps coming from its manifest's
// screen and title rules. herdr reached the same split after running them:
// its integrations for these harnesses report session identity only, and
// their docs say why per harness (docs/integrations.mdx, "How Herdr uses
// integrations").
//
// Sources, per harness, read for this change:
//
//	copilot       https://docs.github.com/en/copilot/reference/hooks-configuration
//	              (~/.copilot/hooks/*.json, SessionStart carries session_id,
//	              sessionStart carries sessionId); herdr assets/copilot
//	cursor-agent  https://cursor.com/docs/hooks (sessionStart); herdr
//	              assets/cursor, which reads session_id or conversation_id
//	devin         herdr assets/devin (SessionStart and UserPromptSubmit carry
//	              session_id or sessionId)
//	droid         herdr assets/droid (SessionStart carries session_id)
//	qoder         https://docs.qoder.com/zh/cli/hooks (Claude Code's shape),
//	              as herdr's installer cites it; herdr assets/qodercli
//	qwen          herdr assets/qwen (SessionStart carries session_id and source)
//	grok          herdr assets/grok (Grok sets GROK_SESSION_ID in every hook
//	              process, and its SessionStart is spelled three ways)
//	antigravity   herdr assets/antigravity_cli (PreInvocation carries
//	              conversationId; stdout must be a JSON object)
//	crush         https://github.com/charmbracelet/crush/blob/main/docs/hooks/README.md
//	              (PreToolUse is the only event; the payload says event and
//	              session_id, and CRUSH_SESSION_ID is set)
//	hermes        herdr assets/hermes (on_session_start and on_session_reset
//	              carry session_id; the tuios plugin forwards them)
//
// Every one of them drops a subagent's event (agent_id set) and an event with
// no session id.

// identityEvents lists, per harness, the events that name the conversation.
var identityEvents = map[string][]string{
	Copilot:     {"SessionStart", "sessionStart"},
	CursorAgent: {"sessionStart"},
	Devin:       {"SessionStart", "UserPromptSubmit"},
	Droid:       {"SessionStart"},
	Qoder:       {"SessionStart"},
	Qwen:        {"SessionStart"},
	Grok:        {"SessionStart", "session_start", "sessionStart"},
	Antigravity: {"PreInvocation"},
	Crush:       {"PreToolUse", "pretooluse", "pre_tool_use"},
	Hermes:      {"on_session_start", "on_session_reset"},
}

// identitySessionKeys lists, per harness, the payload keys that may hold the
// conversation id, in the order they are tried.
var identitySessionKeys = map[string][]string{
	CursorAgent: {"session_id", "sessionId", "conversation_id", "conversationId"},
	Antigravity: {"conversationId", "conversation_id"},
}

func translateIdentity(id string, in Input, p fields) Decision {
	event := eventName(in, p)
	events, ok := identityEvents[id]
	if !ok {
		return skip(id, event, "no hook mapping for harness "+id)
	}
	// Antigravity's PreInvocation payload does not always name its event, and
	// it is the only event tuios registers for it.
	if event == "" && id == Antigravity {
		event = "PreInvocation"
	}
	if event == "" {
		return skip(id, event, "the payload names no event")
	}
	known := false
	for _, e := range events {
		if e == event {
			known = true
			break
		}
	}
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
