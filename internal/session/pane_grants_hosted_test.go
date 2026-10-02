//go:build !slim

package session

import (
	"strings"
	"testing"
)

// TestAHostedPaneReachesNoSessionHereUnderStrict: a process in a pane this
// machine runs for another machine belongs to no session here. Under strict
// its own calls to this daemon reach nothing; under open they are served as
// before.
func TestAHostedPaneReachesNoSessionHereUnderStrict(t *testing.T) {
	d, sp, _, _, b1 := scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return true, "" })
	d.hostedPeer = func(*connState) string { return "hp-1" }
	c := dialVerb(t, sp)
	result(t, callP(c, t, "capture-pane", map[string]any{"session": "b", "window": b1}))

	setStrict(d)
	wantForbidden(t, "capture from a hosted pane", callP(c, t, "capture-pane", map[string]any{"session": "b", "window": b1}))
	wantForbidden(t, "a self report from a hosted pane", callP(c, t, "set-agent-state", map[string]any{"session": "b", "window": b1, "state": "idle"}))
	got := result(t, callP(c, t, "pane-grants", nil))
	if got["pane"] != true || got["session"] != "" {
		t.Errorf("pane-grants from a hosted pane = %v, want a pane with no session", got)
	}

	// A report as the hosted pane passes the grants, to be sent on to the
	// machine that owns it. Here the owner's check is what answers: the test
	// process is not in the pane.
	d.hostedPanesMu.Lock()
	if d.hostedPanes == nil {
		d.hostedPanes = map[string]*hostedPane{}
	}
	d.hostedPanes["hp-1"] = &hostedPane{id: "hp-1", window: "owner-win"}
	d.hostedPanesMu.Unlock()
	t.Cleanup(func() {
		d.hostedPanesMu.Lock()
		delete(d.hostedPanes, "hp-1")
		d.hostedPanesMu.Unlock()
	})
	resp := callP(c, t, "set-agent-state", map[string]any{"window": "owner-win", "state": "idle"})
	msg, _ := resp["error"].(map[string]any)["message"].(string)
	if !strings.Contains(msg, "the caller is not in that pane") {
		t.Errorf("a hosted report was refused before it reached the owner's check: %v", resp)
	}
}
