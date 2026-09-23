package integration

// The maps for Amp, Kimi Code CLI and Pi, the three harnesses outside the
// first four whose hooks cover the whole turn, so tuios takes the pane's state
// from them.

// The Amp event map. Amp loads TypeScript plugins from ~/.config/amp/plugins
// (https://ampcode.com/manual/plugin-api, read for this change). The plugin
// tuios installs (assets/amp/tuios-agent-state.ts) subscribes to session.start,
// agent.start and agent.end, and runs `tuios agent-hook amp` with the event,
// the thread id and agent.end's status. It never subscribes to tool.call,
// whose handler has to answer allow or reject, so installing it cannot change
// what Amp permits. Approval prompts are left to the manifest's screen rules,
// which may override a working report once the prompt is on screen.
//
//	session.start           the thread id only (set-agent-session)
//	agent.start             working
//	agent.end done          done
//	agent.end error         errored
//	agent.end cancelled     idle (the person stopped the turn)
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

// The Kimi Code CLI event map. Source: the hooks reference at
// https://www.kimi.com/code/docs/en/kimi-code-cli/customization/hooks.html,
// read for this change, and herdr's working Kimi asset
// (src/integration/assets/kimi/herdr-agent-state.sh, which needs Kimi Code CLI
// 0.14.0 or newer). Hooks are [[hooks]] tables in ~/.kimi-code/config.toml.
// Every payload carries hook_event_name and session_id; the tool events carry
// tool_name. Exit 0 with empty stdout allows and adds nothing.
//
//	SessionStart                    idle, and the session id
//	UserPromptSubmit                working
//	PreToolUse                      working, except AskUserQuestion, which is
//	                                needs_input, kind question
//	PermissionRequest               needs_input, kind approval
//	PostToolUse, PostToolUseFailure,
//	PermissionResult                working, only if the pane is in needs_input
//	Stop                            done
//	StopFailure                     errored, with the error type
//	Interrupt                       idle (Kimi sends it instead of Stop)
//	SessionEnd                      none
//	subagent and background events: nothing
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
			msg := Clip(p.obj("tool_input").first("question", "prompt"))
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
			msg = "stopped on " + Clip(t)
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

// The Pi event map. Pi loads TypeScript extensions from
// ~/.pi/agent/extensions (PI_CODING_AGENT_DIR overrides the agent directory).
// The extension tuios installs (assets/pi/tuios-agent-state.ts) listens to
// session_start, agent_start and agent_settled, the events herdr's working Pi
// extension uses (src/integration/assets/pi/herdr-agent-state.ts), and only in
// the TUI mode, since the print and RPC modes run with no terminal to show.
//
//	session_start           idle, or working when Pi is mid-turn (a reload
//	                        replaces the extension without a new agent_start),
//	                        with the session id and session file
//	agent_start             working
//	agent_settled           done
func translatePi(in Input, p fields) Decision {
	event := eventName(in, p)
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
	case "agent_settled":
		r.State = "done"
	case "":
		return skip(Pi, event, "the payload names no event")
	default:
		return skip(Pi, event, "event not mapped")
	}
	return send(Pi, event, r)
}
