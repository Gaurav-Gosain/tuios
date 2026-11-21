package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/oneline"
)

func translateCodex(in Input, p fields) Decision {
	event := eventName(in, p)
	if event == "" && p.str("type") == "agent-turn-complete" {
		return send(Codex, "agent-turn-complete", Report{State: "done"})
	}
	if outer := in.env("CODEX_THREAD_ID"); outer != "" && p.str("session_id") != "" && outer != p.str("session_id") {
		return skip(Codex, event, "nested Codex session")
	}
	if p.str("agent_id") != "" {
		return skip(Codex, event, "subagent event")
	}
	switch event {
	case "SessionStart":
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

func withModel(r Report, p fields) Report {
	if r.Activity != nil {
		r.Activity.Model = oneline.Clip(p.str("model"))
	}
	return r
}

const codexTurnContextMax = 8 << 20

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
	if jsonx.Unmarshal(line, &rec) != nil || rec.Type != "turn_context" {
		return false
	}
	switch rec.Payload.ApprovalsReviewer {
	case "auto_review", "guardian_subagent":
	default:
		return false
	}
	var policy string
	if jsonx.Unmarshal(rec.Payload.ApprovalPolicy, &policy) == nil {
		return policy == "on-request"
	}
	var granular map[string]json.RawMessage
	if jsonx.Unmarshal(rec.Payload.ApprovalPolicy, &granular) == nil {
		_, ok := granular["granular"]
		return ok
	}
	return false
}

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
