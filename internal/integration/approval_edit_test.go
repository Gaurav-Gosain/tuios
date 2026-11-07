package integration

import (
	"strings"
	"testing"
)

// Edits offered to the Inbox with their body. These are security-boundary
// tests: each one is a way the Inbox could allow an edit the person did not
// read, and the hook must keep that edit in the pane.

func editPayload(tool string, input map[string]any, suggestions any) map[string]any {
	p := map[string]any{
		"hook_event_name": "PermissionRequest",
		"session_id":      "s1",
		"cwd":             "/w",
		"tool_name":       tool,
		"tool_input":      input,
	}
	if suggestions != nil {
		p["permission_suggestions"] = suggestions
	}
	return p
}

// TestEditDigestIsTheDocumentedEncoding pins edit_sha to the encoding
// docs/protocol.md gives, computed outside Go:
//
//	printf 'tuios-edit-v1\n4:Edit,7:/w/a.go,1:1,7:x := 1\n,7:x := 2\n,1:0,' | shasum -a 256
func TestEditDigestIsTheDocumentedEncoding(t *testing.T) {
	e := EditBody{Tool: "Edit", Path: "/w/a.go", Hunks: []EditHunk{{Old: "x := 1\n", New: "x := 2\n"}}}
	if got := e.Digest(); got != "083b4feca6eb899c647825870da77656ca952ce2dce1db687cf85a68bf7d85de" {
		t.Fatalf("digest %s", got)
	}
	// Field bounds are part of the digest: moving a byte from one string to
	// the next, or flipping replace_all, is another edit.
	for _, other := range []EditBody{
		{Tool: "Edit", Path: "/w/a.go", Hunks: []EditHunk{{Old: "x := 1", New: "\nx := 2\n"}}},
		{Tool: "Edit", Path: "/w/a.go", Hunks: []EditHunk{{Old: "x := 1\n", New: "x := 2\n", ReplaceAll: true}}},
		{Tool: "Write", Path: "/w/a.go", Hunks: []EditHunk{{Old: "x := 1\n", New: "x := 2\n"}}},
		{Tool: "Edit", Path: "/w/b.go", Hunks: []EditHunk{{Old: "x := 1\n", New: "x := 2\n"}}},
	} {
		if other.Digest() == e.Digest() {
			t.Errorf("%+v has the same digest", other)
		}
	}
}

func TestEditsAreOfferedWithTheirBody(t *testing.T) {
	suggest := []any{map[string]any{"type": "addRules", "behavior": "allow", "destination": "session",
		"rules": []any{map[string]any{"toolName": "Edit"}}}}
	for _, tc := range []struct {
		name  string
		tool  string
		input map[string]any
		want  EditBody
		lines int
	}{
		{"edit", "Edit", map[string]any{"file_path": "/w/a.go", "old_string": "a\nb", "new_string": "c", "replace_all": true},
			EditBody{Tool: "Edit", Path: "/w/a.go", Hunks: []EditHunk{{Old: "a\nb", New: "c", ReplaceAll: true}}}, 3},
		{"multiedit", "MultiEdit", map[string]any{"file_path": "/w/a.go", "edits": []any{
			map[string]any{"old_string": "a", "new_string": "b"},
			map[string]any{"old_string": "c", "new_string": "d\te", "replace_all": false},
		}}, EditBody{Tool: "MultiEdit", Path: "/w/a.go", Hunks: []EditHunk{{Old: "a", New: "b"}, {Old: "c", New: "d\te"}}}, 4},
		{"write", "Write", map[string]any{"file_path": "rel/new.go", "content": "package x\n\nfunc f() {}\n"},
			EditBody{Tool: "Write", Path: "rel/new.go", Hunks: []EditHunk{{New: "package x\n\nfunc f() {}\n"}}}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := translatePayload(t, "claude", editPayload(tc.tool, tc.input, suggest))
			a := d.Approval
			if a == nil || a.Edit == nil {
				t.Fatalf("approval %+v, want an edit", a)
			}
			if a.Kind != KindApproval || a.Tool != tc.tool || a.Target != tc.want.Path || a.Cwd != "/w" || !a.DenyMessage {
				t.Errorf("approval %+v", a)
			}
			if a.Edit.Digest() != tc.want.Digest() {
				t.Errorf("body %+v, want %+v", *a.Edit, tc.want)
			}
			if got := a.Edit.Lines(); got != tc.lines {
				t.Errorf("lines %d, want %d", got, tc.lines)
			}
			// Never always: it would allow later edits nobody read.
			if strings.Join(a.Options, ",") != "once,deny" || len(a.Scope) != 0 {
				t.Errorf("options %v scope %v, want once and deny only", a.Options, a.Scope)
			}
			if _, ok := a.Answer("claude-code", DecisionAlways, ""); ok {
				t.Error("always printed a decision")
			}
			if _, ok := a.Answer("claude-code", DecisionOnce, ""); !ok {
				t.Error("once printed nothing")
			}
		})
	}
}

func TestEditsTheInboxDoesNotHold(t *testing.T) {
	big := strings.Repeat("x", MaxEdit)
	for _, tc := range []struct {
		name  string
		tool  string
		input map[string]any
	}{
		{"a notebook edit", "NotebookEdit", map[string]any{"notebook_path": "/w/a.ipynb", "new_source": "x"}},
		{"a body over the bound", "Write", map[string]any{"file_path": "/w/a", "content": big}},
		{"hunks over the bound together", "MultiEdit", map[string]any{"file_path": "/w/a", "edits": []any{
			map[string]any{"old_string": big[:MaxEdit/2], "new_string": "y"},
			map[string]any{"old_string": big[:MaxEdit/2], "new_string": "z"},
		}}},
		{"a carriage return", "Edit", map[string]any{"file_path": "/w/a", "old_string": "a\r\n", "new_string": "b\r\n"}},
		{"an escape", "Write", map[string]any{"file_path": "/w/a", "content": "ok\x1b[2Jhidden"}},
		{"a bidi override", "Edit", map[string]any{"file_path": "/w/a", "old_string": "a", "new_string": "if admin\u202e{ }"}},
		{"a zero width space", "Edit", map[string]any{"file_path": "/w/a", "old_string": "a", "new_string": "x\u200by"}},
		{"a Hangul filler, which draws as nothing", "Edit", map[string]any{"file_path": "/w/a", "old_string": "a", "new_string": "const \u3164 = req.query.\u3164; exec(\u3164)"}},
		{"a braille blank", "Write", map[string]any{"file_path": "/w/a", "content": "x\u2800y"}},
		{"a stack of combining marks", "Write", map[string]any{"file_path": "/w/a", "content": "e" + strings.Repeat("\u0301", 200)}},
		{"a Hangul filler in the path", "Write", map[string]any{"file_path": "/w/a\u3164", "content": "x"}},
		{"invalid UTF-8", "Write", map[string]any{"file_path": "/w/a", "content": "ok\xffno"}},
		{"a NUL", "Write", map[string]any{"file_path": "/w/a", "content": "a\x00b"}},
		{"a newline in the path", "Write", map[string]any{"file_path": "/w/a\nb", "content": "x"}},
		{"no path", "Edit", map[string]any{"old_string": "a", "new_string": "b"}},
		{"an unknown field", "Edit", map[string]any{"file_path": "/w/a", "old_string": "a", "new_string": "b", "mode": "append"}},
		{"an unknown hunk field", "MultiEdit", map[string]any{"file_path": "/w/a", "edits": []any{
			map[string]any{"old_string": "a", "new_string": "b", "line": 3}}}},
		{"no hunks", "MultiEdit", map[string]any{"file_path": "/w/a", "edits": []any{}}},
		{"a hunk that is not an object", "MultiEdit", map[string]any{"file_path": "/w/a", "edits": []any{"a"}}},
		{"a string that is not a string", "Edit", map[string]any{"file_path": "/w/a", "old_string": 3, "new_string": "b"}},
		{"replace_all that is not a bool", "Edit", map[string]any{"file_path": "/w/a", "old_string": "a", "new_string": "b", "replace_all": "yes"}},
		{"an edit with no old_string", "Edit", map[string]any{"file_path": "/w/a", "new_string": "b"}},
		{"a write with no content", "Write", map[string]any{"file_path": "/w/a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := translatePayload(t, "claude", editPayload(tc.tool, tc.input, nil))
			if d.Report == nil || d.Report.State != "needs_input" {
				t.Fatalf("report %+v, want needs_input", d.Report)
			}
			if d.Approval != nil {
				t.Errorf("held: %+v", d.Approval)
			}
		})
	}
	// Exactly the bound is still offered.
	d := translatePayload(t, "claude", editPayload("Write", map[string]any{"file_path": "/w/a", "content": big[:MaxEdit-len("/w/a")]}, nil))
	if d.Approval == nil || d.Approval.Edit == nil {
		t.Error("a body at the bound was not offered")
	}
}
