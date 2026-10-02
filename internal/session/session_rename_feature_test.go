//go:build !slim

package session

import "testing"

// TestGrantedPaneReachesItsSessionByTheOldName: a pane held to its grants
// sends TUIOS_SESSION, which keeps the old name after a rename. The grant
// check compared that name with the pane's live one and refused its own
// session.
func TestGrantedPaneReachesItsSessionByTheOldName(t *testing.T) {
	d, sp, a1, a2, _ := scopeFixture(t)
	setStrict(d)
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	if _, err := d.manager.RenameSession("a", "renamed"); err != nil {
		t.Fatal(err)
	}
	c := dialVerb(t, sp)
	result(t, callP(c, t, "set-agent-state", map[string]any{"session": "a", "state": "working"}))
	result(t, callP(c, t, "list-windows", map[string]any{"session": "a"}))
	result(t, callP(c, t, "send-agent-message", map[string]any{"session": "a", "to": a2, "text": "hi"}))
	if st := d.manager.GetSession("renamed").GetState(); st.Windows[0].AgentState != AgentStateWorking {
		t.Errorf("the pane's report by the old name did not land: %v", st.Windows[0].AgentState)
	}
	// The old name reaches only the renamed session, never another one.
	wantForbidden(t, "list-windows of b", callP(c, t, "list-windows", map[string]any{"session": "b"}))
}
