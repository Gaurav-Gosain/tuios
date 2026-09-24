package integration

import "strings"

// The Codex event map. Sources: the Codex hooks guide
// (https://developers.openai.com/codex/hooks, which now redirects to
// https://learn.chatgpt.com/docs/hooks), read for this change, and herdr's
// working Codex asset (src/integration/assets/codex/herdr-agent-state.sh). The
// guide lists the events (PreToolUse, PermissionRequest, PostToolUse,
// PreCompact, PostCompact, UserPromptSubmit, SubagentStop, Stop, SessionStart,
// SessionEnd, SubagentStart, Interrupt) and the common input fields, which are
// Claude Code's: session_id, transcript_path, cwd, hook_event_name.
//
//	SessionStart          idle, and the session id and transcript path
//	UserPromptSubmit      working
//	PreToolUse            working
//	PermissionRequest     needs_input, kind approval
//	PostToolUse           working, only if the pane is in needs_input
//	Stop                  done
//	Interrupt             idle (the person stopped the turn)
//	SessionEnd            none
//	SubagentStart, SubagentStop, compaction: nothing
//
// The prompt, tool and Stop events also carry activity for the pane's ring,
// from the fields the generated hook schemas in the Codex repository list
// (codex-rs/hooks/schema/generated): user-prompt-submit's prompt,
// pre-tool-use's and post-tool-use's tool_name and tool_input, stop's
// last_assistant_message, and the model every event carries. PostToolUse
// runs only after a tool produced its output, so it is a tool_done; its ok
// comes from an exit_code in tool_response when there is one. Codex has no
// failure event. apply_patch carries its patch in tool_input.command, and the
// files come from the patch's header lines.
//
// PermissionRequest runs before Codex's own reviewer may approve the call
// itself, so it is a state and never a decision point: the hook prints
// nothing, and a pane an auto-review approved is moved on by PostToolUse.
//
// The legacy notify command (a top-level `notify` in config.toml) hands its
// JSON as the last argument instead, with "type": "agent-turn-complete" and a
// "thread-id". It is read too, so a user who wired notify by hand gets done
// reports, but the installer writes hooks: notify allows one command only and
// says nothing but that a turn completed.

func translateCodex(in Input, p fields) Decision {
	event := eventName(in, p)
	if event == "" && p.str("type") == "agent-turn-complete" {
		return send(Codex, "agent-turn-complete", Report{State: "done", SessionID: p.str("thread-id")})
	}
	// A Codex started by a tool call inside another Codex inherits the outer
	// one's CODEX_THREAD_ID. herdr's asset drops the event when the two differ,
	// and so does this.
	if outer := in.env("CODEX_THREAD_ID"); outer != "" && p.str("session_id") != "" && outer != p.str("session_id") {
		return skip(Codex, event, "nested Codex session")
	}
	if p.str("agent_id") != "" {
		return skip(Codex, event, "subagent event")
	}
	switch event {
	case "SessionStart":
		return send(Codex, event, identity(Report{State: "idle"}, p))
	case "UserPromptSubmit":
		r := identity(Report{State: "working"}, p)
		if text := activityText(p.str("prompt")); text != "" {
			r.Activity = &Activity{Event: ActivityPrompt, Text: text}
		}
		return send(Codex, event, withModel(r, p))
	case "PreToolUse":
		r := identity(Report{State: "working"}, p)
		r.Activity = toolActivity(ActivityTool, p)
		return send(Codex, event, withModel(r, p))
	case "PermissionRequest":
		msg := "approve " + ToolSummary(p.str("tool_name"), p.obj("tool_input"))
		return send(Codex, event, identity(Report{State: "needs_input", Kind: "approval", Message: msg}, p))
	case "PostToolUse":
		r := identity(Report{State: "working", IfState: claudeClearsBlock}, p)
		if a := toolActivity(ActivityToolDone, p); a != nil {
			if code, ok := exitCode(p); ok {
				a.OK = boolPtr(code == 0)
			}
			r.Activity = a
		}
		return send(Codex, event, withModel(r, p))
	case "Stop":
		return send(Codex, event, withModel(turnEnd(identity(Report{State: "done"}, p), p), p))
	case "Interrupt":
		return send(Codex, event, identity(Report{State: "idle"}, p))
	case "SessionEnd":
		return send(Codex, event, identity(Report{State: "none"}, p))
	case "":
		return skip(Codex, event, "the payload names no event")
	default:
		if strings.HasPrefix(event, "Subagent") {
			return skip(Codex, event, "subagent event")
		}
		return skip(Codex, event, "event not mapped")
	}
}

// withModel names the model a Codex event carries on the report's activity.
// Every Codex hook event has one; an event with no activity carries none.
func withModel(r Report, p fields) Report {
	if r.Activity != nil {
		r.Activity.Model = Clip(p.str("model"))
	}
	return r
}
