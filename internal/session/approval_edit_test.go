package session

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/risk"
)

// Edits held with their body. These are security-boundary tests: each one is
// a way an allow from the Inbox could reach an edit the person did not read.

var (
	testEdit = integration.EditBody{Tool: "Edit", Path: "notes.txt", Hunks: []integration.EditHunk{
		{Old: "retries = 3\n", New: "retries = 5\n"},
	}}
	testOtherEdit = integration.EditBody{Tool: "Edit", Path: "notes.txt", Hunks: []integration.EditHunk{
		{Old: "retries = 3\n", New: "retries = 500\n"},
	}}
)

func editLine(e integration.EditBody) string {
	return "approve " + e.Tool + ": " + e.Path
}

// editParams is the request-approval a hook sends for e.
func editParams(window string, e integration.EditBody, extra map[string]any) map[string]any {
	p := map[string]any{
		"session": "work", "window": window, "harness": "claude", "summary": editLine(e),
		"options": []string{"once", "deny"}, "tool": e.Tool, "target": e.Path, "deny_message": true,
		"edit": e,
	}
	maps.Copy(p, extra)
	return p
}

// holdEdit reports the pane blocked on e and holds it.
func holdEdit(t *testing.T, c *verbConn, sp, window string, e integration.EditBody, extra map[string]any) (<-chan map[string]any, map[string]any) {
	t.Helper()
	setAgentState(t, c, "work", window, "needs_input", "approval", editLine(e))
	pending, _ := requestApprovalWith(t, sp, editParams(window, e, extra))
	return pending, heldItem(t, c, window)
}

func stillHeld(t *testing.T, pending <-chan map[string]any, why string) {
	t.Helper()
	select {
	case resp := <-pending:
		t.Fatalf("ASSERTION: the hold ended on %s: %v", why, resp)
	default:
	}
}

// TestEditIsHeldAndAnsweredByDigest: an edit is held with its body, its
// digest and line count on the item and its body served by get-approval.
// Always is never offered. An allow must name the digest of the body shown;
// without it, with a wrong one, or with the digest of another edit, nothing is
// answered and the hold runs on. A deny needs no digest.
//
// Negative control: with the edit_sha check removed from answer, the allow
// without edit_sha is applied and the hook gets once.
func TestEditIsHeldAndAnsweredByDigest(t *testing.T) {
	d, sp := startTestDaemon(t)
	enableRiskyApprovals(t, d, 30*time.Second, nil)
	_, a, b := riskSession(t, d)
	makeSessionWithWindow(t, d, "other")
	c := dialVerb(t, sp)
	tui := attachTUI(t, sp, "other")

	// The hook asks for always; an edit never gets it.
	pending, it := holdEdit(t, c, sp, a, testEdit, map[string]any{
		"options": []string{"once", "always", "deny"}, "always_scope": []string{"Edit (every call) for this session"},
	})
	id := it["request_id"].(string)
	sha := testEdit.Digest()
	if it["kind"] != AttentionApproval || it["edit_sha"] != sha || it["edit_lines"] != float64(2) || it["deny_message"] != true {
		t.Fatalf("the held edit is %v", it)
	}
	if fmt.Sprint(it["options"]) != "[once deny]" || it["always_scope"] != nil || it["risk"] != nil {
		t.Fatalf("the held edit is %v, want once and deny with no scope and no risk", it)
	}

	// A report from the pane while the hold runs keeps what the hold put on
	// the item: a client must not lose edit_sha and show an allow.
	setAgentState(t, c, "work", a, "needs_input", "approval", riskyLine)
	for _, x := range waitAttention(t, c, "the item after a new report", func(items []map[string]any) bool { return len(items) > 0 }) {
		if x["window"] == a {
			it = x
		}
	}
	if it["edit_sha"] != sha || it["edit_lines"] != float64(2) || it["request_id"] != id {
		t.Fatalf("ASSERTION: after a report the held edit is %v", it)
	}

	got := result(t, callP(c, t, "get-approval", map[string]any{"request_id": id}))
	edit, _ := got["edit"].(map[string]any)
	hunks, _ := edit["hunks"].([]any)
	if got["edit_sha"] != sha || edit["tool"] != "Edit" || edit["path"] != "notes.txt" || len(hunks) != 1 || got["untrusted"] != true {
		t.Fatalf("get-approval answered %v", got)
	}
	if h, _ := hunks[0].(map[string]any); h["old"] != "retries = 3\n" || h["new"] != "retries = 5\n" {
		t.Fatalf("get-approval served the hunk %v", hunks[0])
	}

	for _, extra := range []map[string]any{{}, {"edit_sha": ""}, {"edit_sha": "0123"}, {"edit_sha": testOtherEdit.Digest()}, {"plan_sha": sha}} {
		resp := replyWith(c, t, id, ApprovalOnce, tui.HumanNonce(), extra)
		mustRefuse(t, resp, ErrVerbEditUnreviewed, fmt.Sprintf("an allow with %v", extra))
	}
	mustRefuse(t, replyWith(c, t, id, ApprovalAlways, tui.HumanNonce(), map[string]any{"edit_sha": sha}), ErrVerbInvalidParams, "always on an edit")
	stillHeld(t, pending, "an allow without the digest")

	res := result(t, replyWith(c, t, id, ApprovalOnce, tui.HumanNonce(), map[string]any{"edit_sha": sha, "summary": editLine(testEdit)}))
	if res["applied"] != true {
		t.Fatalf("the allow with the digest answered %v", res)
	}
	if got := awaitResult(t, pending); got["decision"] != ApprovalOnce {
		t.Fatalf("the hook got %v", got)
	}
	if code := errCode(t, callP(c, t, "get-approval", map[string]any{"request_id": id})); code != ErrVerbInvalidParams {
		t.Errorf("get-approval after the answer: %s, want invalid_params", code)
	}

	// A deny takes no digest and carries its reason.
	pending, it = holdEdit(t, c, sp, b, testEdit, nil)
	res = result(t, replyWith(c, t, it["request_id"].(string), ApprovalDeny, tui.HumanNonce(), map[string]any{"message": "keep 3"}))
	if res["applied"] != true {
		t.Fatalf("the deny answered %v", res)
	}
	if got := awaitResult(t, pending); got["decision"] != ApprovalDeny || got["message"] != "keep 3" {
		t.Fatalf("the hook got %v", got)
	}
}

// TestEditCannotBeSwappedWhileHeld: a second request for the pane with
// another body ends the first hold. The digest of the first body allows
// neither hold: the old one has ended, and the new one is another edit.
func TestEditCannotBeSwappedWhileHeld(t *testing.T) {
	d, sp := startTestDaemon(t)
	enableRiskyApprovals(t, d, 30*time.Second, nil)
	_, a, _ := riskSession(t, d)
	makeSessionWithWindow(t, d, "other")
	c := dialVerb(t, sp)
	tui := attachTUI(t, sp, "other")

	first, it := holdEdit(t, c, sp, a, testEdit, nil)
	oldID, oldSHA := it["request_id"].(string), it["edit_sha"].(string)

	second, _ := requestApprovalWith(t, sp, editParams(a, testOtherEdit, nil))
	if got := awaitResult(t, first); got["reason"] != approvalEndSuperseded || got["decision"] != "" {
		t.Fatalf("the first hold ended %v, want superseded with no decision", got)
	}
	items := waitAttention(t, c, "the second hold", func(items []map[string]any) bool {
		return slices.ContainsFunc(items, func(it map[string]any) bool {
			return it["window"] == a && it["request_id"] != nil && it["request_id"] != oldID
		})
	})
	for _, x := range items {
		if x["window"] == a {
			it = x
		}
	}
	newID := it["request_id"].(string)
	if it["edit_sha"] != testOtherEdit.Digest() {
		t.Fatalf("the second hold is %v", it)
	}

	mustRefuse(t, replyWith(c, t, oldID, ApprovalOnce, tui.HumanNonce(), map[string]any{"edit_sha": oldSHA}), ErrVerbInvalidParams, "an allow of the ended hold")
	mustRefuse(t, replyWith(c, t, newID, ApprovalOnce, tui.HumanNonce(), map[string]any{"edit_sha": oldSHA}), ErrVerbEditUnreviewed, "the first body's digest on the second hold")
	stillHeld(t, second, "the first body's digest")
	res := result(t, replyWith(c, t, newID, ApprovalOnce, tui.HumanNonce(), map[string]any{"edit_sha": testOtherEdit.Digest()}))
	if res["applied"] != true {
		t.Fatalf("the allow answered %v", res)
	}
	if got := awaitResult(t, second); got["decision"] != ApprovalOnce {
		t.Fatalf("the hook got %v", got)
	}
}

// TestRiskyEditNeedsTheAcknowledgement: the risk rules read an edit's path as
// they read any file tool's, so an edit outside the worktree needs risk_ack
// as well as edit_sha.
func TestRiskyEditNeedsTheAcknowledgement(t *testing.T) {
	d, sp := startTestDaemon(t)
	enableRiskyApprovals(t, d, 30*time.Second, nil)
	_, a, _ := riskSession(t, d)
	makeSessionWithWindow(t, d, "other")
	c := dialVerb(t, sp)
	tui := attachTUI(t, sp, "other")

	e := integration.EditBody{Tool: "Write", Path: "/etc/hosts", Hunks: []integration.EditHunk{{New: "127.0.0.1 example.com\n"}}}
	pending, it := holdEdit(t, c, sp, a, e, nil)
	id, sha := it["request_id"].(string), e.Digest()
	if !slices.Contains(itemRisk(it), risk.RuleOutsideWorktree) {
		t.Fatalf("the held edit is %v, want it marked outside the worktree", it)
	}
	rules := itemRisk(it)
	mustRefuse(t, replyWith(c, t, id, ApprovalOnce, tui.HumanNonce(), map[string]any{"edit_sha": sha}), ErrVerbRiskUnacknowledged, "a risky edit without risk_ack")
	mustRefuse(t, replyWith(c, t, id, ApprovalOnce, tui.HumanNonce(), map[string]any{"risk_ack": rules}), ErrVerbEditUnreviewed, "a risky edit without edit_sha")
	stillHeld(t, pending, "an allow missing one of the two")
	res := result(t, replyWith(c, t, id, ApprovalOnce, tui.HumanNonce(), map[string]any{"edit_sha": sha, "risk_ack": rules}))
	if res["applied"] != true {
		t.Fatalf("the allow answered %v", res)
	}
	if got := awaitResult(t, pending); got["decision"] != ApprovalOnce {
		t.Fatalf("the hook got %v", got)
	}
}

// TestEditRequestRefusals: the daemon checks the body again, because the
// person answers from what it serves: over the bound, characters that would
// make it read as something else, a body that is not the call the line and
// the risk rules read, and edit fields on a plan are all refused.
func TestEditRequestRefusals(t *testing.T) {
	d, sp := startTestDaemon(t)
	enableRiskyApprovals(t, d, 30*time.Second, nil)
	_, a, _ := riskSession(t, d)
	c := dialVerb(t, sp)
	setAgentState(t, c, "work", a, "needs_input", "approval", editLine(testEdit))

	with := func(mut func(e *integration.EditBody)) integration.EditBody {
		e := testEdit
		e.Hunks = slices.Clone(testEdit.Hunks)
		mut(&e)
		return e
	}
	big := strings.Repeat("x", integration.MaxEdit)
	for _, tc := range []struct {
		why    string
		params map[string]any
	}{
		{"a body over the bound", editParams(a, with(func(e *integration.EditBody) { e.Hunks[0].New = big }), nil)},
		{"a carriage return", editParams(a, with(func(e *integration.EditBody) { e.Hunks[0].New = "x\r\n" }), nil)},
		{"an escape", editParams(a, with(func(e *integration.EditBody) { e.Hunks[0].New = "\x1b[2J" }), nil)},
		{"a bidi override", editParams(a, with(func(e *integration.EditBody) { e.Hunks[0].Old = "a‮b" }), nil)},
		{"invalid UTF-8", editParams(a, with(func(e *integration.EditBody) { e.Hunks[0].New = "\xff" }), nil)},
		{"no hunks", editParams(a, with(func(e *integration.EditBody) { e.Hunks = nil }), nil)},
		{"a tool that is not an edit", editParams(a, with(func(e *integration.EditBody) { e.Tool = "Read" }), map[string]any{"summary": "approve Read: notes.txt"})},
		{"a write that replaces text", editParams(a, with(func(e *integration.EditBody) { e.Tool = "Write" }), map[string]any{"summary": "approve Write: notes.txt"})},
		{"a target that is not the edit's path", editParams(a, testEdit, map[string]any{"target": "other.txt"})},
		{"a tool that is not the edit's tool", editParams(a, testEdit, map[string]any{"tool": "Write"})},
		{"a line that is not the edit's", editParams(a, testEdit, map[string]any{"summary": "approve Edit: other.txt"})},
		{"an edit on a plan", editParams(a, testEdit, map[string]any{"kind": "plan", "plan": "# x"})},
		// A file tool with no body would be held as a plain approval,
		// allowed from its path alone.
		{"an edit tool with no body", editParams(a, testEdit, map[string]any{"edit": nil})},
		{"a notebook edit", map[string]any{"session": "work", "window": a, "harness": "claude", "summary": "approve NotebookEdit: a.ipynb", "tool": "NotebookEdit", "target": "a.ipynb"}},
		{"an edit line with no tool and no body", map[string]any{"session": "work", "window": a, "harness": "claude", "summary": editLine(testEdit)}},
		{"an edit line beside another tool", map[string]any{"session": "work", "window": a, "harness": "claude", "summary": editLine(testEdit), "tool": "Bash", "target": "ls"}},
		{"another harness's file tool with no body", map[string]any{"session": "work", "window": a, "harness": "claude", "summary": "approve write: a.go", "tool": "write", "target": "a.go"}},
	} {
		mustRefuse(t, callP(c, t, "request-approval", tc.params), ErrVerbInvalidParams, tc.why)
	}
}
