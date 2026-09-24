package integration

import "strings"

// The Claude Code event map. Source: the hooks reference at
// https://code.claude.com/docs/en/hooks, read for this change. It lists the
// events, the common input fields (session_id, transcript_path, cwd,
// hook_event_name, agent_id "only in subagent context"), Notification's
// notification_type values, PermissionRequest's tool_name and tool_input,
// SessionStart's source, StopFailure's error_type and SessionEnd's reason.
//
// What each event means for a pane:
//
//	SessionStart          idle, and the session id and transcript path
//	                      (source compact is mid-turn and reports nothing)
//	UserPromptSubmit      working
//	PreToolUse            working
//	PermissionRequest     needs_input, kind approval
//	PostToolUse           working, only if the pane is in needs_input
//	PostToolUseFailure    working, only if the pane is in needs_input
//	PermissionDenied      working, only if the pane is in needs_input
//	ElicitationResult     working, only if the pane is in needs_input
//	Notification          by notification_type, see claudeNotification
//	Stop                  done
//	StopFailure           errored, with the error_type
//	SessionEnd            none
//	SubagentStop and any event with agent_id: nothing
//
// Five of them also carry activity for the pane's ring, read from the fields
// the reference documents for them:
//
//	UserPromptSubmit      prompt: the first line of prompt
//	PreToolUse            tool: tool_name, and what tool_input names
//	PostToolUse           tool_done, ok: the files an edit tool wrote
//	PostToolUseFailure    tool_failed: the first line of error
//	Stop                  turn_end: the first line of last_assistant_message,
//	                      which is also the done report's message, or no
//	                      text when the field is missing or empty
//
// PermissionRequest is the approval signal. Notification's permission_prompt
// also fires for one, but only after the user seems away, which is too late
// to be the only signal. The Post* events are what clears a block once it is
// answered: before them nothing moved a pane off needs_input until the turn
// ended.

// claudeClearsBlock is the if_state for an event that means a block was
// answered. It must not turn a finished or idle pane back to working.
const claudeClearsBlock = "needs_input"

func translateClaude(in Input, p fields) Decision {
	event := eventName(in, p)
	// Cursor reads Claude Code's hook configuration too, and runs the same
	// commands for its own agent. herdr's Claude asset filters on the same two
	// markers (src/integration/assets/claude/herdr-agent-state.sh).
	if in.env("CURSOR_VERSION") != "" || p.str("cursor_version") != "" {
		return skip(ClaudeCode, event, "foreign harness: the event comes from Cursor")
	}
	// Grok CLI imports Claude Code's hooks as well, and sets GROK_SESSION_ID
	// in every hook process it starts. herdr's Claude asset narrowed its
	// SessionStart matcher for the same reason (Grok sends source new and
	// load, herdr docs, integrations.mdx).
	if in.env("GROK_SESSION_ID") != "" {
		return skip(ClaudeCode, event, "foreign harness: the event comes from Grok")
	}
	if p.str("agent_id") != "" {
		return skip(ClaudeCode, event, "subagent event")
	}
	switch event {
	case "SessionStart":
		if p.str("source") == "compact" {
			return skip(ClaudeCode, event, "compaction restarts the session mid-turn")
		}
		return send(ClaudeCode, event, identity(Report{State: "idle"}, p))
	case "UserPromptSubmit":
		r := identity(Report{State: "working"}, p)
		if text := activityText(p.str("prompt")); text != "" {
			r.Activity = &Activity{Event: ActivityPrompt, Text: text}
		}
		return send(ClaudeCode, event, r)
	case "PreToolUse":
		r := identity(Report{State: "working"}, p)
		r.Activity = toolActivity(ActivityTool, p)
		return send(ClaudeCode, event, r)
	case "PermissionRequest":
		msg := "approve " + ToolSummary(p.str("tool_name"), p.obj("tool_input"))
		if p.str("tool_name") == claudePlanTool {
			// A plan is known by its title, not by the tool's name.
			msg = PlanSummaryPrefix + PlanTitle(p.obj("tool_input").str("plan"))
		}
		d := send(ClaudeCode, event, identity(Report{State: "needs_input", Kind: "approval", Message: msg}, p))
		d.Approval = claudeApproval(p)
		return d
	case "PostToolUse", "PostToolUseFailure", "PermissionDenied", "ElicitationResult":
		r := identity(Report{State: "working", IfState: claudeClearsBlock}, p)
		switch event {
		case "PostToolUse":
			if a := toolActivity(ActivityToolDone, p); a != nil {
				a.OK = boolPtr(true)
				r.Activity = a
			}
		case "PostToolUseFailure":
			if a := toolActivity(ActivityToolFailed, p); a != nil {
				// orca's Claude reader takes error, then message.
				a.OK = boolPtr(false)
				a.Text = activityText(p.first("error", "message"))
				r.Activity = a
			}
		}
		return send(ClaudeCode, event, r)
	case "Notification":
		return claudeNotification(event, p)
	case "Stop":
		return send(ClaudeCode, event, turnEnd(identity(Report{State: "done"}, p), p))
	case "StopFailure":
		msg := "stopped on an error"
		if t := p.str("error_type"); t != "" {
			msg = "stopped on " + t
		}
		return send(ClaudeCode, event, identity(Report{State: "errored", Message: msg}, p))
	case "SessionEnd":
		return send(ClaudeCode, event, identity(Report{State: "none"}, p))
	case "SubagentStop":
		return skip(ClaudeCode, event, "subagent event")
	case "":
		return skip(ClaudeCode, event, "the payload names no event")
	default:
		return skip(ClaudeCode, event, "event not mapped")
	}
}

// claudeNotification maps a Notification by its notification_type.
//
//	permission_prompt                      needs_input, kind approval
//	elicitation_dialog, elicitation_url_dialog,
//	agent_needs_input                      needs_input, kind question
//	idle_prompt                            idle, only if the pane is working
//	                                       or unknown
//	auth_success, elicitation_complete, elicitation_response,
//	agent_completed, quota_*               nothing
//
// idle_prompt fires after the prompt has sat unanswered for a while. It says
// nothing new about a pane Stop already marked done, and done has to stay so
// the person still sees the turn finished, so it only corrects a pane still
// showing working after a Stop that never arrived.
//
// A Claude Code old enough to send no notification_type is read from its
// message, and only for the two messages it is known to send. Anything else
// reports nothing, which is the fix for the old shim mapping every
// notification, auth_success included, to needs_input.
func claudeNotification(event string, p fields) Decision {
	msg := strings.TrimSpace(p.str("message"))
	kind := p.str("notification_type")
	if kind == "" {
		lower := strings.ToLower(msg)
		switch {
		case strings.Contains(lower, "needs your permission"):
			kind = "permission_prompt"
		case strings.Contains(lower, "waiting for your input"):
			kind = "idle_prompt"
		default:
			return skip(ClaudeCode, event, "notification with no type and an unknown message")
		}
	}
	switch kind {
	case "permission_prompt":
		return send(ClaudeCode, event, identity(Report{State: "needs_input", Kind: "approval", Message: Clip(msg)}, p))
	case "elicitation_dialog", "elicitation_url_dialog", "agent_needs_input":
		return send(ClaudeCode, event, identity(Report{State: "needs_input", Kind: "question", Message: Clip(msg)}, p))
	case "idle_prompt":
		return send(ClaudeCode, event, identity(Report{State: "idle", IfState: "working,unknown"}, p))
	default:
		return skip(ClaudeCode, event, "notification type "+kind+" is not a state change")
	}
}

// turnEnd adds a Stop event's activity to its done report: the first line of
// last_assistant_message, which also becomes the report's message, so a
// finished pane says what it finished with. Claude Code and Codex both send
// the field. A Stop without it, from an older Claude Code or a turn that
// ended with no text, still reports turn_end, with no text and no message, so
// the ring records the end of the turn and the daemon clears what the agent
// was doing.
func turnEnd(r Report, p fields) Report {
	text := activityText(p.str("last_assistant_message"))
	if text != "" {
		r.Message = text
	}
	r.Activity = &Activity{Event: ActivityTurnEnd, Text: text}
	return r
}
