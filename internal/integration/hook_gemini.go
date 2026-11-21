package integration

import "github.com/Gaurav-Gosain/tuios/internal/oneline"

func translateGemini(in Input, p fields) Decision {
	event := eventName(in, p)
	switch event {
	case "SessionStart":
		return send(GeminiCLI, event, identity(Report{State: "idle"}, p))
	case "BeforeAgent":
		return send(GeminiCLI, event, identity(Report{State: "working"}, p))
	case "BeforeTool":
		r := identity(Report{State: "working"}, p)
		if tool := oneline.Clip(p.str("tool_name")); tool != "" {
			r.Activity = &Activity{Event: ActivityTool, Tool: tool}
		}
		return send(GeminiCLI, event, r)
	case "AfterTool":
		return send(GeminiCLI, event, identity(Report{State: "working", IfState: claudeClearsBlock}, p))
	case "Notification":
		if p.str("notification_type") != "ToolPermission" {
			return skip(GeminiCLI, event, "notification type "+p.str("notification_type")+" is not a state change")
		}
		msg := oneline.Clip(p.str("message"))
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
