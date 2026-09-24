package integration

// The Gemini CLI event map. Source: the hooks reference at
// https://geminicli.com/docs/hooks/reference/, read for this change. It lists
// the events (BeforeTool, AfterTool, BeforeAgent, AfterAgent, BeforeModel,
// BeforeToolSelection, AfterModel, SessionStart, SessionEnd, Notification,
// PreCompress), the common input fields (session_id, transcript_path, cwd,
// hook_event_name, timestamp), and Notification's notification_type
// "ToolPermission" with a message and details. It also says a hook must print
// nothing but its JSON answer on stdout, which is why `tuios agent-hook
// gemini-cli` prints an empty object.
//
//	SessionStart          idle, and the session id and transcript path
//	BeforeAgent           working (a prompt was submitted)
//	BeforeTool            working
//	AfterTool             working, only if the pane is in needs_input
//	Notification          needs_input, kind approval, for ToolPermission
//	AfterAgent            done
//	SessionEnd            none
//	model and compression events: nothing
//
// BeforeTool also carries its tool_name as tool activity, for the rail's
// "now" line. Nothing else in a Gemini payload is read for activity, since
// nothing else was verified against the reference.

func translateGemini(in Input, p fields) Decision {
	event := eventName(in, p)
	switch event {
	case "SessionStart":
		return send(GeminiCLI, event, identity(Report{State: "idle"}, p))
	case "BeforeAgent":
		return send(GeminiCLI, event, identity(Report{State: "working"}, p))
	case "BeforeTool":
		r := identity(Report{State: "working"}, p)
		if tool := Clip(p.str("tool_name")); tool != "" {
			r.Activity = &Activity{Event: ActivityTool, Tool: tool}
		}
		return send(GeminiCLI, event, r)
	case "AfterTool":
		return send(GeminiCLI, event, identity(Report{State: "working", IfState: claudeClearsBlock}, p))
	case "Notification":
		if p.str("notification_type") != "ToolPermission" {
			return skip(GeminiCLI, event, "notification type "+p.str("notification_type")+" is not a state change")
		}
		msg := Clip(p.str("message"))
		if msg == "" {
			msg = "approve a tool call"
		}
		return send(GeminiCLI, event, identity(Report{State: "needs_input", Kind: "approval", Message: msg}, p))
	case "AfterAgent":
		return send(GeminiCLI, event, identity(Report{State: "done"}, p))
	case "SessionEnd":
		return send(GeminiCLI, event, identity(Report{State: "none"}, p))
	case "":
		return skip(GeminiCLI, event, "the payload names no event")
	default:
		return skip(GeminiCLI, event, "event not mapped")
	}
}
