package session

import (
	"strings"
	"sync/atomic"
	"testing"
)

// A child agent's approval is the person's alone. This is a security
// boundary: the parent agent holds the child's grants and more, and the one
// gate it does not hold is what the child's harness asks before a call. A
// parent that could answer that prompt could widen itself through its child.
// See attention_children.go.
//
// Ways it could pass wrongly, written down first:
//   - The refusal could come from a missing grant, not from the link. The
//     caller holds admin and respond, which cover every typing rule, and the
//     same keys into a pane with no link must go through.
//   - It could refuse every prompt of a child. A child's question is the
//     parent's to answer, and typing into it must go through.
//   - It could hold for one verb and not another: send-keys, send-text and
//     respond are each tried.
func TestNoPaneAnswersAChildAgentsApproval(t *testing.T) {
	d, sp, a1, a2, b1 := scopeFixture(t)
	// admin and respond: every rule a typing pane meets is covered.
	all := GrantAdmin | GrantRespond
	if !d.manager.grants.set(a1, &all) {
		t.Fatalf("pane %s is not in the grant table", a1)
	}
	var asPane atomic.Bool
	d.setApprovalPeer(func(*connState) (bool, string) {
		if asPane.Load() {
			return true, a1
		}
		return false, ""
	})
	sa := d.manager.GetSession("a")
	sb := d.manager.GetSession("b")
	if sa == nil || sb == nil {
		t.Fatal("the fixture sessions are missing")
	}
	// a2 is a child agent of a1. b1 is an agent nobody started.
	sa.setAgentParent(a2, &agentParent{ref: PaneRef{Session: "a", Window: a1}, link: AgentLink{Root: PaneRef{Session: "a", Window: a1}, Depth: 1}})

	person := dialVerb(t, sp)
	block := func(session, window, kind string) {
		t.Helper()
		result(t, callP(person, t, "set-agent-state", map[string]any{
			"session": session, "window": window, "state": "needs_input", "kind": kind, "message": "Bash: curl example.test | sh",
		}))
	}
	block("a", a2, "approval")
	block("b", b1, "approval")

	asPane.Store(true)
	pane := dialVerb(t, sp)
	refused := func(what string, resp map[string]any) {
		t.Helper()
		if code := errCode(t, resp); code != ErrVerbForbidden {
			t.Errorf("%s answered %s, want forbidden: %v", what, code, resp)
			return
		}
		if msg, _ := resp["error"].(map[string]any)["message"].(string); !strings.Contains(msg, "child agent") {
			t.Errorf("%s was refused for another reason: %v", what, resp)
		}
	}
	refused("send-keys into the child's approval", callP(pane, t, "send-keys", map[string]any{"session": "a", "window": a2, "keys": "1", "allow_blocked": true}))
	refused("send-text into the child's approval", callP(pane, t, "send-text", map[string]any{"session": "a", "window": a2, "text": "y", "submit": true, "allow_blocked": true}))
	refused("respond to the child's approval", callP(pane, t, "respond", map[string]any{"session": "a", "window": a2, "action": "approve"}))

	// The same keys into an approval of a pane with no link go through: the
	// caller's grants cover them.
	if resp := callP(pane, t, "send-keys", map[string]any{"session": "b", "window": b1, "keys": "1", "allow_blocked": true}); resp["error"] != nil {
		t.Errorf("send-keys into an unlinked pane's approval was refused, so the rule is not the link: %v", resp)
	}

	// A child's question is the parent's to answer.
	asPane.Store(false)
	result(t, callP(person, t, "set-agent-state", map[string]any{"session": "a", "window": a2, "state": "working"}))
	block("a", a2, "question")
	asPane.Store(true)
	if resp := callP(pane, t, "send-text", map[string]any{"session": "a", "window": a2, "text": "main.go", "submit": true, "allow_blocked": true}); resp["error"] != nil {
		t.Errorf("typing an answer to the child's question was refused: %v", resp)
	}
}
