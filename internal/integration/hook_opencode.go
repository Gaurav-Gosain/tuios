package integration

import "github.com/Gaurav-Gosain/tuios/internal/oneline"

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
			if msg := oneline.Clip(p.str("retry_message")); msg != "" {
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
			r.Message = "approve " + ToolSummary(p.str("tool"), p.obj("tool_input"))
		case p.str("title") != "":
			r.Message = "approve " + oneline.Clip(p.str("title"))
		default:
			r.Message = "approve a tool call"
		}
	case "question.asked":
		r.State, r.Kind, r.Message = "needs_input", "question", oneline.Clip(p.str("title"))
	case "permission.replied", "question.replied", "question.rejected":
		r.State, r.IfState = "working", claudeClearsBlock
	case "session.idle":
		r.State, r.IfState = "done", openCodeTurnStates
	case "session.error":
		r.State = "errored"
		r.Message = oneline.Clip(p.str("error"))
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
