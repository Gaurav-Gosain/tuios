package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
)

// The Codex event map. Sources: the Codex hooks guide
// (https://developers.openai.com/codex/hooks, which now redirects to
// https://learn.chatgpt.com/docs/hooks), read for this change, and herdr's
// working Codex asset (src/integration/assets/codex/herdr-agent-state.sh). The
// guide lists the events (PreToolUse, PermissionRequest, PostToolUse,
// PreCompact, PostCompact, UserPromptSubmit, SubagentStop, Stop, SessionStart,
// SessionEnd, SubagentStart, Interrupt) and the common input fields, which are
// Claude Code's: session_id, transcript_path, cwd, hook_event_name.
//
//	SessionStart          idle, and the session id and transcript path;
//	                      session_start unless the source is compact
//	UserPromptSubmit      working
//	PreToolUse            working
//	PermissionRequest     needs_input, kind approval, unless Codex's
//	                      automatic reviewer decides the request
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
// nothing. With approvals_reviewer = "auto_review" and an on-request or
// granular approval policy, Codex hands every such request to its automatic
// reviewer, which allows or denies it without a person (codex-rs/ext/
// guardian-reviewer, routes_approval_policy_to_guardian). Reporting
// needs_input there raised an approval alert for every tool call the reviewer
// then allowed (#1183), so the event reports nothing for such a turn. The
// payload does not say who reviews; the rollout does, in the turn_context
// record Codex writes at the start of each turn (codexAutoReviews). A prompt
// that does reach the person is still read from the screen, the title and
// Codex's notification.
//
// The legacy notify command (a top-level `notify` in config.toml) hands its
// JSON as the last argument instead, with "type": "agent-turn-complete" and a
// "thread-id". It is read too, so a user who wired notify by hand gets done
// reports, but the installer writes hooks: notify allows one command only and
// says nothing but that a turn completed.

func translateCodex(in Input, p fields) Decision {
	event := eventName(in, p)
	if event == "" && p.str("type") == "agent-turn-complete" {
		// No session id: the legacy notify fires for every conversation the
		// app-server runs, and its report must not be refused by the
		// shared-server guard, which only the hooks' SessionStart arms.
		return send(Codex, "agent-turn-complete", Report{State: "done"})
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
		// Codex sends SessionStart with source compact after every
		// compaction (startup, resume, clear and fork are the others). That is
		// the same conversation going on, not a new one, so it must not take
		// the pane over from the shared-server guard.
		start := p.str("source") != "compact"
		return send(Codex, event, identity(Report{State: "idle", SessionStart: start}, p))
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
		if codexAutoReviews(p.str("transcript_path")) {
			return skip(Codex, event, "Codex's automatic reviewer decides this request")
		}
		msg := "approve " + ToolSummary(p.str("tool_name"), p.obj("tool_input"))
		return send(Codex, event, identity(Report{State: "needs_input", Kind: "approval", Message: msg}, p))
	case "PostToolUse":
		r := identity(Report{State: "working", IfState: claudeClearsBlock}, p)
		if a := toolActivity(ActivityToolDone, p); a != nil {
			if code, ok := exitCode(p); ok {
				a.OK = new(code == 0)
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

// codexTurnContextMax bounds how far back from the end of a rollout
// codexAutoReviews looks for the turn's turn_context record. A turn whose tool
// output pushed the record further back than this reports as before.
const codexTurnContextMax = 8 << 20

// codexAutoReviews reports whether the Codex turn the rollout at path is on
// sends its approval requests to the automatic reviewer: the last turn_context
// record names approvals_reviewer auto_review (or its old name,
// guardian_subagent) with an approval policy of on-request or granular, the
// two that Codex routes to the reviewer. Anything it cannot read or does not
// recognise answers false, so the request is reported as an approval, as it
// was before: a missed prompt is worse than a needless one.
func codexAutoReviews(path string) bool {
	if path == "" {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	line := lastTurnContext(f, info.Size())
	if line == nil {
		return false
	}
	var rec struct {
		Type    string `json:"type"`
		Payload struct {
			ApprovalPolicy    json.RawMessage `json:"approval_policy"`
			ApprovalsReviewer string          `json:"approvals_reviewer"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &rec) != nil || rec.Type != "turn_context" {
		return false
	}
	switch rec.Payload.ApprovalsReviewer {
	case "auto_review", "guardian_subagent":
	default:
		return false
	}
	var policy string
	if json.Unmarshal(rec.Payload.ApprovalPolicy, &policy) == nil {
		return policy == "on-request"
	}
	var granular map[string]json.RawMessage
	if json.Unmarshal(rec.Payload.ApprovalPolicy, &granular) == nil {
		_, ok := granular["granular"]
		return ok
	}
	return false
}

// lastTurnContext returns the last whole line of the rollout that is a
// turn_context record, reading back from the end in growing windows up to
// codexTurnContextMax. Codex writes each record as compact JSON, so the
// record's own type field is the only place the unescaped text below occurs:
// inside a string its quotes would be escaped.
func lastTurnContext(r io.ReaderAt, size int64) []byte {
	marker := []byte(`"type":"turn_context"`)
	for window := int64(64 << 10); ; window *= 4 {
		window = min(window, size, codexTurnContextMax)
		buf := make([]byte, window)
		if _, err := r.ReadAt(buf, size-window); err != nil && err != io.EOF {
			return nil
		}
		if i := bytes.LastIndex(buf, marker); i >= 0 {
			start := bytes.LastIndexByte(buf[:i], '\n') + 1
			// A line cut at the window's start is read again from a wider one.
			if start > 0 || window == size {
				end := len(buf)
				if j := bytes.IndexByte(buf[i:], '\n'); j >= 0 {
					end = i + j
				}
				return buf[start:end]
			}
		}
		if window == size || window == codexTurnContextMax {
			return nil
		}
	}
}
