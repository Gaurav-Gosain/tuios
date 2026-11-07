package tuie2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// editApprovalAgent asks to edit notes.txt through the Claude Code hook, as
// the installed integration does, and writes the hook's answer.
func editApprovalAgent(out string) string {
	return `#!/bin/bash
echo "asking to edit notes.txt"
printf '%s' '{"hook_event_name":"PermissionRequest","session_id":"s-1","tool_name":"Edit","tool_input":{"file_path":"notes.txt","old_string":"retries = 3\n","new_string":"retries = 5\n"}}' \
  | TUIOS_AGENT=claude-code "` + tuiosBin + `" agent-hook claude-code > ` + out + `.tmp 2>&1
mv ` + out + `.tmp ` + out + `
sleep 600
`
}

// editBody is an edit as get-approval serves it.
type editBody struct {
	Tool  string `json:"tool"`
	Path  string `json:"path"`
	Hunks []struct {
		Old        string `json:"old"`
		New        string `json:"new"`
		ReplaceAll bool   `json:"replace_all"`
	} `json:"hunks"`
}

// editDigest is edit_sha computed here from the encoding docs/protocol.md
// gives, not from the daemon's code, so the test also checks the document.
func editDigest(e editBody) string {
	var b strings.Builder
	b.WriteString("tuios-edit-v1\n")
	field := func(s string) {
		b.WriteString(strconv.Itoa(len(s)))
		b.WriteByte(':')
		b.WriteString(s)
		b.WriteByte(',')
	}
	field(e.Tool)
	field(e.Path)
	field(strconv.Itoa(len(e.Hunks)))
	for _, h := range e.Hunks {
		field(h.Old)
		field(h.New)
		if h.ReplaceAll {
			field("1")
		} else {
			field("0")
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// TestGUIBridgeEditApprovalNeedsItsDigest: a Claude Code Edit sent through
// the real hook is held with its body. The bridge, the path guios reads,
// carries edit_sha on the item and get-approval serves the body. An allow
// with no edit_sha, or a wrong one, is refused with edit_unreviewed and the
// hook waits on; always is not offered; the allow that names the digest of
// the body served reaches the hook as allow.
//
// The steps and the hook's answer are saved under artifactDir as
// edit-approval.json.
//
// Negative control (e2e/tui/NEGATIVE_CONTROLS.md): with the edit_sha check
// removed from the daemon's answer, the first allow is applied and the test
// fails at "an allow with no edit_sha".
func TestGUIBridgeEditApprovalNeedsItsDigest(t *testing.T) {
	base := t.TempDir()
	pinPreV080Looks(t, base)
	cfg := configPathIn(base)
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	data = append(data, []byte("\n[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 120\n")...)
	writeConfigAtomically(t, cfg, data)

	b := startBridge(t, base, "edit", 160, 48)
	hook := filepath.Join(workDirIn(t, base), "hook.json")
	agent := newPaneRunning(t, base, "edit", "editor", writeAgentScript(t, base, "edit.sh", editApprovalAgent(hook)))

	var steps []map[string]any
	step := func(name string, r wireVerbResult) {
		steps = append(steps, map[string]any{"step": name, "ok": r.OK, "code": r.Code, "result": json.RawMessage(nonEmptyJSON(r.Result))})
	}
	defer func() {
		hookOut, _ := os.ReadFile(hook)
		out, _ := json.MarshalIndent(map[string]any{"steps": steps, "hook": string(hookOut)}, "", "  ")
		_ = os.WriteFile(filepath.Join(artifactDir(t), "edit-approval.json"), out, 0o644)
	}()

	var item wireAttentionItem
	b.waitEvent(func(e *wireInboxEvent) bool {
		if e.Type != "attention" || e.Attention == nil {
			return false
		}
		for _, it := range e.Attention.Items {
			if it.Window == agent && it.Kind == "approval" && it.RequestID != "" {
				item = it
				return true
			}
		}
		return false
	}, "an attention event with the held edit", shellTimeout)

	r := b.verb("get-approval", map[string]any{"request_id": item.RequestID})
	step("get-approval", r)
	if !r.OK {
		t.Fatalf("get-approval: %s %s", r.Code, r.Error)
	}
	var detail struct {
		Edit    *editBody `json:"edit"`
		EditSHA string    `json:"edit_sha"`
		Options []string  `json:"options"`
		Summary string    `json:"summary"`
	}
	if err := json.Unmarshal(r.Result, &detail); err != nil || detail.Edit == nil {
		t.Fatalf("get-approval served no edit: %s", r.Result)
	}
	if detail.Edit.Path != "notes.txt" || len(detail.Edit.Hunks) != 1 || detail.Edit.Hunks[0].Old != "retries = 3\n" || detail.Edit.Hunks[0].New != "retries = 5\n" {
		t.Fatalf("get-approval served %+v", *detail.Edit)
	}
	sha := editDigest(*detail.Edit)
	if detail.EditSHA != sha || item.EditSHA != sha || item.EditLines != 2 {
		t.Fatalf("edit_sha %q on get-approval, %q on the item (%d lines); the documented encoding gives %q", detail.EditSHA, item.EditSHA, item.EditLines, sha)
	}
	if slices.Contains(item.Options, "always") {
		t.Fatalf("always is offered on the item: %v", item.Options)
	}
	if slices.Contains(detail.Options, "always") {
		t.Fatalf("always is offered on an edit: %v", detail.Options)
	}

	// The pane here has no root, so the rules read the path as outside the
	// worktree, and every allow acknowledges that as a GUI would.
	for _, tc := range []struct {
		name  string
		extra map[string]any
	}{
		{"an allow with no edit_sha", nil},
		{"an allow with a wrong edit_sha", map[string]any{"edit_sha": strings.Repeat("0", 64)}},
	} {
		// risk_ack rides along, so edit_sha is the only thing missing.
		params := map[string]any{"request_id": item.RequestID, "decision": "once", "summary": detail.Summary}
		if len(item.Risk) > 0 {
			params["risk_ack"] = item.Risk
		}
		for k, v := range tc.extra {
			params[k] = v
		}
		r := b.verb("reply-approval", params)
		step(tc.name, r)
		if r.OK || r.Code != "edit_unreviewed" {
			t.Fatalf("ASSERTION: %s: ok=%v code=%q", tc.name, r.OK, r.Code)
		}
		if _, err := os.Stat(hook); err == nil {
			t.Fatalf("ASSERTION: the hook answered after %s", tc.name)
		}
	}

	allow := map[string]any{"request_id": item.RequestID, "decision": "once", "summary": detail.Summary, "edit_sha": sha}
	if len(item.Risk) > 0 {
		allow["risk_ack"] = item.Risk
	}
	r = b.verb("reply-approval", allow)
	step("an allow with the edit_sha", r)
	if !r.OK {
		t.Fatalf("reply-approval with edit_sha: %s %s", r.Code, r.Error)
	}
	got := waitFile(t, hook, func(s string) bool { return s != "" }, "the hook's answer")
	if !strings.Contains(got, `"behavior":"allow"`) || strings.Contains(got, "updatedPermissions") {
		t.Fatalf("the hook did not get a plain allow: %s", got)
	}
}

func nonEmptyJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}
