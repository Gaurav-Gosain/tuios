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
//	session.status busy or retry    working
//	session.status idle             nothing (session.idle says more)
//	permission.asked                needs_input, kind approval
//	question.asked                  needs_input, kind question
//	permission.replied, question.replied,
//	question.rejected               working, only if the pane is in needs_input
//	session.idle                    done
//	session.error                   errored
//	session.deleted                 none

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
		case "busy", "retry", "running", "working", "pending":
			r.State = "working"
		default:
			return skip(id, event, "status "+p.str("status")+" is left to session.idle")
		}
	case "permission.asked", "permission.updated":
		r.State, r.Kind = "needs_input", "approval"
		r.Message = "approve " + Clip(p.str("title"))
		if p.str("title") == "" {
			r.Message = "approve a tool call"
		}
	case "question.asked":
		r.State, r.Kind, r.Message = "needs_input", "question", Clip(p.str("title"))
	case "permission.replied", "question.replied", "question.rejected":
		r.State, r.IfState = "working", claudeClearsBlock
	case "session.idle":
		r.State = "done"
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
	return send(id, event, r)
}
