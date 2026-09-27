package integration

// The opencode event map. opencode has no command hooks. It loads JavaScript
// plugins from its plugins directory (https://opencode.ai/docs/plugins/), and
// the plugin tuios installs (assets/opencode/tuios-agent-state.js) runs
// `tuios agent-hook opencode` with a small JSON object on stdin for the bus
// events it listens to. The event names are opencode's own, as herdr's working
// plugin handles them (src/integration/assets/opencode/herdr-agent-state.js).
// The plugin drops events from child sessions, the subagents opencode starts,
// before they get here.
//
// Kilo Code CLI is a fork of opencode with the same plugin API and bus events,
// loading plugins from its own plugin directory. herdr's Kilo plugin
// (src/integration/assets/kilo/herdr-agent-state.js) handles the same event
// names, so Kilo gets the same plugin and this same map under its own id.
//
//	session.created                 idle, and the session id
//	chat.message, tool.execute.before,
//	session.status busy             working
//	session.status retry            working, with the retry's reason
//	session.status idle             done, only if the pane is working or in
//	                                needs_input
//	permission.asked                needs_input, kind approval
//	question.asked                  needs_input, kind question
//	permission.replied, question.replied,
//	question.rejected               working, only if the pane is in needs_input
//	session.idle                    done, only if the pane is working or in
//	                                needs_input
//	session.error                   errored
//	session.deleted                 none
//
// session.status idle is the end of a turn. opencode's schema marks
// session.idle deprecated in its favour (packages/schema/src/
// session-status-event.ts), and still sends both for now. Both mean done only
// for a pane in a turn: an idle status also comes at rest, and a pane that is
// already done must not report the same turn twice. So the second of the two
// events a turn ends with changes nothing.

// openCodeTurnStates are the states a pane is in during a turn, the ones an
// idle status ends.
const openCodeTurnStates = "working,needs_input"

func translateOpenCode(id string, in Input, p fields) Decision {
	event := eventName(in, p)
	r := Report{SessionID: p.str("session_id")}
	switch event {
	case "session.created":
		r.State = "idle"
	case "chat.message", "tool.execute.before":
		r.State = "working"
	case "session.status":
		switch p.str("status") {
		case "busy", "running", "working", "pending":
			r.State = "working"
		case "retry":
			r.State = "working"
			if msg := Clip(p.str("retry_message")); msg != "" {
				r.Message = "retrying: " + msg
			}
		case "idle":
			r.State, r.IfState = "done", openCodeTurnStates
		default:
			return skip(id, event, "status "+p.str("status")+" is not a state change")
		}
	case "permission.asked", "permission.updated":
		r.State, r.Kind = "needs_input", "approval"
		switch {
		case p.str("tool") != "":
			// The version 2 plugin names the tool call the request is
			// about, which says more than opencode's permission name.
			r.Message = "approve " + ToolSummary(p.str("tool"), p.obj("tool_input"))
		case p.str("title") != "":
			r.Message = "approve " + Clip(p.str("title"))
		default:
			r.Message = "approve a tool call"
		}
	case "question.asked":
		r.State, r.Kind, r.Message = "needs_input", "question", Clip(p.str("title"))
	case "permission.replied", "question.replied", "question.rejected":
		r.State, r.IfState = "working", claudeClearsBlock
	case "session.idle":
		r.State, r.IfState = "done", openCodeTurnStates
	case "session.error":
		r.State = "errored"
		r.Message = Clip(p.str("error"))
	case "session.deleted":
		r.State = "none"
	case "":
		return skip(id, event, "the payload names no event")
	default:
		return skip(id, event, "event not mapped")
	}
	d := send(id, event, r)
	if r.Kind == "approval" {
		d.Approval = openCodeApproval(p)
	}
	return d
}
