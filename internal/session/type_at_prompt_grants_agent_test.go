//go:build !slim

package session

import "testing"

// MsgTypeAtPrompt types into a pane, so a pane that sends it is held to the
// check its keystrokes get: an admin pane cannot type into a pane waiting on
// a prompt, which the respond grant guards.
func TestAnAdminPaneCannotCdIntoAPromptOverTheClientProtocol(t *testing.T) {
	d, sp, a1, a2, _ := scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	person := dialVerb(t, sp)
	setAgentState(t, person, "a", a2, string(AgentStateNeedsInput), "approval", "approve Bash: rm -rf build")
	sess := d.manager.GetSession("a")

	if got := cdAsPane(t, d, sess, a1, ptyOf(t, sess, a2), "/tmp"); got.Typed || got.Refused == "" {
		t.Fatalf("an admin pane's cd into a pane on a prompt = %+v, want refused", got)
	}
}
