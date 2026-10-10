package integration

import (
	"slices"
	"strings"
)

// The Claude Code event map. Source: the hooks reference at
// https://code.claude.com/docs/en/hooks, read for this change. It lists the
// events, the common input fields (session_id, transcript_path, cwd,
// hook_event_name, agent_id "only in subagent context"), Notification's
// notification_type values, PermissionRequest's tool_name and tool_input,
// SessionStart's source, StopFailure's error_type, SessionEnd's reason, and
// SubagentStart's and SubagentStop's agent_id and agent_type.
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
//	SubagentStart         no state: a subagent started, see claudeSubagent
//	SubagentStop          no state: a subagent stopped
//	PreToolUse, PostToolUse, PostToolUseFailure with agent_id
//	                      no state: a tool call inside a subagent
//	any other event with agent_id: nothing
//
// Eight of them also carry activity for the pane's ring, read from the fields
// the reference documents for them:
//
//	SessionStart          session_start: source (startup, resume or clear)
//	UserPromptSubmit      prompt: the first line of prompt
//	PreToolUse            tool: tool_name, and what tool_input names
//	PostToolUse           tool_done, ok: the files an edit tool wrote
//	PostToolUseFailure    tool_failed: the first line of error
//	Stop                  turn_end: the first line of last_assistant_message,
//	                      which is also the done report's message, or no
//	                      text when the field is missing or empty
//	SubagentStart         subagent_start: agent_id and agent_type
//	SubagentStop          subagent_stop: agent_id, agent_type and the first
//	                      line of last_assistant_message
//
// The last two go with report-agent-activity rather than on a state report
// (see StateActivity). The daemon keeps the subagents started on the pane,
// and forgets them at a session_start, so the rail can say work goes on in a
// pane whose main agent finished its turn. Claude Code 2.1.286, measured: a
// subagent's start and stop carry the same agent_id and the main session's
// session_id, and SubagentStop fires for a background subagent too, and for
// one that errors or is stopped. An agent-team teammate fires both in its
// lead's session, with its name as agent_type: a start each time it wakes to
// work, a stop when it goes idle.
//
// What each subagent does is a subagent_update, also sent with
// report-agent-activity, from these (Claude Code 2.1.296, captured):
//
//	PreToolUse of Agent   the tool activity carries spawn: the subagent_type
//	(or Task)             and tool_use_id, with the description as its
//	                      target. SubagentStart names neither, so the daemon
//	                      gives them to the next subagent that starts.
//	PostToolUse of Agent  tool_response.agentId with its description, which
//	                      corrects that guess, and totalToolUseCount once a
//	                      foreground subagent completed
//	PostToolUseFailure    outcome failed with the error, by tool_use_id,
//	of Agent              since the failure names no agent
//	PreToolUse of TaskStop
//	(KillShell, KillBash) outcome stopping for tool_input.task_id
//	a tool event with     the subagent's tool and target, and on a Post event
//	agent_id              how the call ended
//	Stop, SubagentStop    one update per subagent in background_tasks, the
//	                      running tasks, with its description
//
// A subagent's own tool calls name it by agent_id, which the reference says
// to use, not agent_type, to tell a subagent's call from the main thread's.
// Nothing reads the undocumented files Claude Code writes beside a subagent's
// transcript.
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
		switch event {
		case "SubagentStart", "SubagentStop":
			return claudeSubagent(event, p)
		case "PreToolUse", "PostToolUse", "PostToolUseFailure":
			return claudeSubagentTool(event, p)
		}
		return skip(ClaudeCode, event, "subagent event")
	}
	switch event {
	case "SessionStart":
		source := p.str("source")
		if source == "compact" {
			return skip(ClaudeCode, event, "compaction restarts the session mid-turn")
		}
		r := identity(Report{State: "idle"}, p)
		r.Activity = &Activity{Event: ActivitySessionStart, Text: activityText(source)}
		return send(ClaudeCode, event, r)
	case "UserPromptSubmit":
		r := identity(Report{State: "working"}, p)
		if text := activityText(p.str("prompt")); text != "" {
			r.Activity = &Activity{Event: ActivityPrompt, Text: text}
		}
		return send(ClaudeCode, event, r)
	case "PreToolUse":
		r := identity(Report{State: "working"}, p)
		r.Activity = toolActivity(ActivityTool, p)
		if r.Activity != nil && slices.Contains(claudeSpawnTools, r.Activity.Tool) {
			claudeSpawnHint(r.Activity, p)
		}
		if slices.Contains(claudeStopTools, p.str("tool_name")) {
			// The main agent stops a background task. The id may be a
			// shell's, which the daemon does not know and so ignores.
			if id := p.obj("tool_input").str("task_id"); ValidSubagentID(id) {
				r.Extra = append(r.Extra, Activity{Event: ActivitySubagentUpdate, AgentID: id, Outcome: OutcomeStopping})
			}
		}
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
		if slices.Contains(claudeSpawnTools, p.str("tool_name")) {
			if u, ok := claudeSpawnResult(event, p); ok {
				r.Extra = append(r.Extra, u)
			}
		}
		return send(ClaudeCode, event, r)
	case "Notification":
		return claudeNotification(event, p)
	case "Stop":
		r := turnEnd(identity(Report{State: "done"}, p), p)
		r.Extra = claudeBackgroundTasks(p)
		return send(ClaudeCode, event, r)
	case "StopFailure":
		msg := "stopped on an error"
		if t := p.str("error_type"); t != "" {
			msg = "stopped on " + t
		}
		return send(ClaudeCode, event, identity(Report{State: "errored", Message: msg}, p))
	case "SessionEnd":
		return send(ClaudeCode, event, identity(Report{State: "none"}, p))
	case "SubagentStart", "SubagentStop":
		return skip(ClaudeCode, event, "subagent event with no agent_id")
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

// claudeSubagent reports a subagent starting or stopping. The report is its
// activity alone, with no state, which the hook sends with
// report-agent-activity: the main agent's own events say what the pane is
// doing, and a subagent's start or stop, which can come while the pane is
// done, says nothing about that. The session id rides along so the daemon's
// identity guard keeps a nested run's subagents off the pane.
func claudeSubagent(event string, p fields) Decision {
	id := p.str("agent_id")
	if !ValidSubagentID(id) {
		return skip(ClaudeCode, event, "agent_id is not an id tuios keeps: 1 to 128 letters, digits, '_', '.', ':', '@' or '-'")
	}
	a := &Activity{Event: ActivitySubagentStart, AgentID: id, AgentType: activityText(p.str("agent_type"))}
	r := Report{SessionID: p.str("session_id"), Activity: a}
	if event == "SubagentStop" {
		a.Event = ActivitySubagentStop
		// What the subagent said last. The daemon reads an empty one as a
		// subagent that was stopped rather than one that finished.
		a.Text = activityText(p.str("last_assistant_message"))
		r.Extra = claudeBackgroundTasks(p)
	}
	return send(ClaudeCode, event, r)
}

// claudeSpawnTools are the names Claude Code gives the tool that launches a
// subagent: Agent, and Task in builds before 2.1.
var claudeSpawnTools = []string{"Agent", "Task"}

// claudeStopTools are the names Claude Code gives the tool that stops a
// background task: TaskStop, and the older KillShell and KillBash.
var claudeStopTools = []string{"TaskStop", "KillShell", "KillBash"}

// claudeBackgroundMax bounds the subagents one event's background_tasks
// reports, each one a call to the daemon.
const claudeBackgroundMax = 16

// claudeSpawnHint marks a PreToolUse of the Agent tool as the launch of a
// subagent: the type asked for, and the call's id. Its Target is already the
// description (toolTargetKeys reads it). SubagentStart carries neither, so the
// daemon gives them to the next subagent that starts.
func claudeSpawnHint(a *Activity, p fields) {
	a.Spawn = true
	a.AgentType = activityText(p.obj("tool_input").str("subagent_type"))
	if id := p.str("tool_use_id"); ValidSubagentID(id) {
		a.CallID = id
	}
}

// claudeSpawnResult is the subagent_update a PostToolUse or
// PostToolUseFailure of the Agent tool makes. The PostToolUse names the
// subagent it started in tool_response.agentId, with the description it was
// given, which corrects the daemon's guess at the start. A finished
// foreground subagent's response also counts its tool calls. A failure names
// no subagent, so it goes by the call's id, with the error.
func claudeSpawnResult(event string, p fields) (Activity, bool) {
	callID := p.str("tool_use_id")
	if !ValidSubagentID(callID) {
		callID = ""
	}
	if event == "PostToolUseFailure" {
		if callID == "" {
			return Activity{}, false
		}
		return Activity{Event: ActivitySubagentUpdate, CallID: callID, Outcome: OutcomeFailed, Text: activityText(p.first("error", "message"))}, true
	}
	resp := p.obj("tool_response")
	id := resp.str("agentId")
	if !ValidSubagentID(id) {
		return Activity{}, false
	}
	u := Activity{Event: ActivitySubagentUpdate, AgentID: id, CallID: callID}
	u.Text = activityText(resp.first("description"))
	if u.Text == "" {
		u.Text = activityText(p.obj("tool_input").str("description"))
	}
	if n, ok := resp["totalToolUseCount"].(float64); ok && resp.str("status") == "completed" && n > 0 && n < 1e6 {
		u.Tools = int(n)
	}
	return u, true
}

// claudeSubagentTool reports a tool call inside a subagent, which Claude Code
// fires with the subagent's agent_id: a subagent_update with the tool and
// what it acts on, and on a Post event how the call ended. A subagent that
// launches one of its own sends the spawn hint on the same update, so the
// nested subagent is named when it starts.
func claudeSubagentTool(event string, p fields) Decision {
	id := p.str("agent_id")
	if !ValidSubagentID(id) {
		return skip(ClaudeCode, event, "agent_id is not an id tuios keeps: 1 to 128 letters, digits, '_', '.', ':', '@' or '-'")
	}
	a := toolActivity(ActivityTool, p)
	if a == nil {
		return skip(ClaudeCode, event, "subagent tool event with no tool_name")
	}
	a.Event, a.AgentID = ActivitySubagentUpdate, id
	switch event {
	case "PreToolUse":
		if slices.Contains(claudeSpawnTools, a.Tool) {
			claudeSpawnHint(a, p)
		}
	case "PostToolUse":
		a.OK = boolPtr(true)
	case "PostToolUseFailure":
		a.OK = boolPtr(false)
	}
	return send(ClaudeCode, event, Report{SessionID: p.str("session_id"), Activity: a})
}

// claudeBackgroundTasks reads the background_tasks list that Stop and
// SubagentStop carry: one entry per task still running, with its id, which
// for a subagent is its agent_id, its type and its description. Each
// subagent's description is sent as a subagent_update, which corrects a
// description the daemon guessed when two subagents of one type started
// together.
func claudeBackgroundTasks(p fields) []Activity {
	list, _ := p["background_tasks"].([]any)
	var out []Activity
	for _, v := range list {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		t := fields(m)
		id := t.str("id")
		text := activityText(t.str("description"))
		if t.str("type") != "subagent" || !ValidSubagentID(id) || text == "" {
			continue
		}
		out = append(out, Activity{Event: ActivitySubagentUpdate, AgentID: id, Text: text})
		if len(out) == claudeBackgroundMax {
			break
		}
	}
	return out
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
