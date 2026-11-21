package integration

import (
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/oneline"
)

const claudeClearsBlock = "needs_input"

const claudeNotBlocked = "none,working,idle,done,errored,unknown"

func translateClaude(in Input, p fields) Decision {
	event := eventName(in, p)
	if in.env("CURSOR_VERSION") != "" || p.str("cursor_version") != "" {
		return skip(ClaudeCode, event, "foreign harness: the event comes from Cursor")
	}
	if in.env("GROK_SESSION_ID") != "" {
		return skip(ClaudeCode, event, "foreign harness: the event comes from Grok")
	}
	if p.str("agent_id") != "" {
		switch event {
		case "SubagentStart", "SubagentStop":
			return claudeSubagent(event, p)
		case "PermissionRequest":
			return claudeSubagentAsk(event, p)
		case "PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionDenied":
			return claudeSubagentTool(event, p)
		}
		return skip(ClaudeCode, event, "subagent event")
	}
	switch event {
	case "SessionStart":
		source := p.str("source")
		if source == "compact" {
			return skip(ClaudeCode, event, SkipCompact)
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
			if id := p.obj("tool_input").str("task_id"); ValidSubagentID(id) {
				r.Extra = append(r.Extra, Activity{Event: ActivitySubagentUpdate, AgentID: id, Outcome: OutcomeStopping})
			}
		}
		return send(ClaudeCode, event, r)
	case "PermissionRequest":
		if q, ok := claudeQuestion(p); ok {
			return send(ClaudeCode, event, identity(Report{State: "needs_input", Kind: "question", Message: q, Notification: true}, p))
		}
		msg := "approve " + ToolSummary(p.str("tool_name"), p.obj("tool_input"))
		if p.str("tool_name") == claudePlanTool {
			msg = PlanSummaryPrefix + PlanTitle(p.obj("tool_input").str("plan"))
		}
		d := send(ClaudeCode, event, identity(Report{State: "needs_input", Kind: "approval", Message: msg, Notification: true}, p))
		d.Approval = claudeApproval(p)
		return d
	case "PostToolUse", "PostToolUseFailure", "PermissionDenied", "ElicitationResult":
		r := identity(Report{State: "working", IfState: claudeClearsBlock}, p)
		switch event {
		case "PostToolUse":
			if a := toolActivity(ActivityToolDone, p); a != nil {
				a.OK = new(true)
				r.Activity = a
			}
		case "PostToolUseFailure":
			if a := toolActivity(ActivityToolFailed, p); a != nil {
				a.OK = new(false)
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
		return send(ClaudeCode, event, identity(claudeStopFailure(p), p))
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

func claudeQuestion(p fields) (string, bool) {
	if p.str("tool_name") != "AskUserQuestion" {
		return "", false
	}
	if qs, ok := p.obj("tool_input")["questions"].([]any); ok && len(qs) > 0 {
		if q, ok := qs[0].(map[string]any); ok {
			if text, _ := q["question"].(string); oneline.Clip(text) != "" {
				return oneline.Clip(text), true
			}
		}
	}
	return "AskUserQuestion", true
}

var claudeLimitErrors = []string{"rate_limit", "billing_error"}

func claudeStopFailure(p fields) Report {
	errType := p.first("error", "error_type")
	if slices.Contains(claudeLimitErrors, errType) {
		full := p.str("last_assistant_message")
		msg := activityText(full)
		if msg == "" {
			msg = "Stopped on the usage limit."
		}
		return Report{State: "limit_reached", Message: harness.LimitMessage(msg, harness.LimitReset(full))}
	}
	if errType == "" {
		return Report{State: "errored", Message: "stopped on an error"}
	}
	return Report{State: "errored", Message: "stopped on " + errType}
}

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
		return send(ClaudeCode, event, identity(Report{State: "needs_input", Kind: "approval", Message: oneline.Clip(msg), IfState: claudeNotBlocked, Notification: true}, p))
	case "elicitation_dialog", "agent_needs_input":
		return send(ClaudeCode, event, identity(Report{State: "needs_input", Kind: "question", Message: oneline.Clip(msg), Notification: true}, p))
	case "elicitation_url_dialog":
		return send(ClaudeCode, event, identity(Report{State: "needs_input", Kind: "question", Message: oneline.Clip(msg)}, p))
	case "idle_prompt":
		return send(ClaudeCode, event, identity(Report{State: "idle", IfState: "working,unknown"}, p))
	default:
		return skip(ClaudeCode, event, "notification type "+kind+" is not a state change")
	}
}

func claudeSubagent(event string, p fields) Decision {
	id := p.str("agent_id")
	if !ValidSubagentID(id) {
		return skip(ClaudeCode, event, "agent_id is not an id tuios keeps: 1 to 128 letters, digits, '_', '.', ':', '@' or '-'")
	}
	a := &Activity{Event: ActivitySubagentStart, AgentID: id, AgentType: activityText(p.str("agent_type"))}
	r := Report{SessionID: p.str("session_id"), Activity: a}
	if event == "SubagentStop" {
		a.Event = ActivitySubagentStop
		a.Text = activityText(p.str("last_assistant_message"))
		r.Extra = claudeBackgroundTasks(p)
	}
	return send(ClaudeCode, event, r)
}

var claudeSpawnTools = []string{"Agent", "Task"}

var claudeStopTools = []string{"TaskStop", "KillShell", "KillBash"}

const claudeBackgroundMax = 16

func claudeSpawnHint(a *Activity, p fields) {
	a.Spawn = true
	a.AgentType = activityText(p.obj("tool_input").str("subagent_type"))
	if id := p.str("tool_use_id"); ValidSubagentID(id) {
		a.CallID = id
	}
}

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

func claudeSubagentAsk(event string, p fields) Decision {
	id := p.str("agent_id")
	if !ValidSubagentID(id) {
		return skip(ClaudeCode, event, "agent_id is not an id tuios keeps: 1 to 128 letters, digits, '_', '.', ':', '@' or '-'")
	}
	msg := "approve " + ToolSummary(p.str("tool_name"), p.obj("tool_input"))
	return send(ClaudeCode, event, Report{State: "needs_input", Kind: "approval", Message: msg, SubagentID: id, SessionID: p.str("session_id")})
}

func claudeSubagentTool(event string, p fields) Decision {
	id := p.str("agent_id")
	if !ValidSubagentID(id) {
		return skip(ClaudeCode, event, "agent_id is not an id tuios keeps: 1 to 128 letters, digits, '_', '.', ':', '@' or '-'")
	}
	answered := Report{SessionID: p.str("session_id"), State: "working", IfState: claudeClearsBlock, IfSubagentID: id}
	if event == "PermissionDenied" {
		return send(ClaudeCode, event, answered)
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
		a.OK = new(true)
	case "PostToolUseFailure":
		a.OK = new(false)
	}
	if event == "PreToolUse" {
		return send(ClaudeCode, event, Report{SessionID: p.str("session_id"), Activity: a})
	}
	answered.Activity = a
	return send(ClaudeCode, event, answered)
}

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

func turnEnd(r Report, p fields) Report {
	text := oneline.Clip(SaidLine(p.str("last_assistant_message")))
	if text != "" {
		r.Message = text
	}
	r.Activity = &Activity{Event: ActivityTurnEnd, Text: text}
	return r
}
