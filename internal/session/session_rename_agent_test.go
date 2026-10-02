//go:build !slim

package session

import "testing"

// TestRestrictedConnectionFollowsARename: tuios mcp restricts its connection
// to its own session. The restriction held the session's name, so after a
// rename every event and every call of that session was out of reach.
func TestRestrictedConnectionFollowsARename(t *testing.T) {
	d, sp, a1, _, _ := scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	c := dialVerb(t, sp)
	restrict(t, c, map[string]any{"read_only": true})
	result(t, callP(c, t, "subscribe", map[string]any{"types": []string{EventAgentState}}))

	if _, err := d.manager.RenameSession("a", "renamed"); err != nil {
		t.Fatal(err)
	}
	plain := dialVerb(t, sp)
	setAgentState(t, plain, "renamed", a1, "working", "", "")
	if ev := readEvent(t, c); ev["session"] != "renamed" || ev["window"] != a1 {
		t.Fatalf("event after the rename = %v, want the renamed session's", ev)
	}

	calls := dialVerb(t, sp)
	res := restrict(t, calls, map[string]any{"scope": "own"})
	if res["session"] != "renamed" {
		t.Fatalf("restrict after the rename names session %v, want renamed", res["session"])
	}
	if _, err := d.manager.RenameSession("renamed", "again"); err != nil {
		t.Fatal(err)
	}
	listed := result(t, callP(calls, t, "list-agents", nil))
	if listed["session"] != "again" {
		t.Errorf("list-agents on a restricted connection after a rename listed %v, want again", listed["session"])
	}
}

// TestSubscriberFollowsARename: a stream on one session keeps getting its
// events under the new name, and does not get the old name's close.
func TestSubscriberFollowsARename(t *testing.T) {
	d, sp, a1, _, _ := scopeFixture(t)
	c := dialVerb(t, sp)
	result(t, callP(c, t, "subscribe", map[string]any{"session": "a"}))
	if _, err := d.manager.RenameSession("a", "renamed"); err != nil {
		t.Fatal(err)
	}
	plain := dialVerb(t, sp)
	setAgentState(t, plain, "renamed", a1, "working", "", "")
	for {
		ev := readEvent(t, c)
		if ev["type"] == EventSessionClosed {
			t.Fatalf("the stream got %v, which reads as the end of its session", ev)
		}
		if ev["type"] == EventAgentState {
			if ev["session"] != "renamed" {
				t.Errorf("agent state event = %v, want session renamed", ev)
			}
			return
		}
	}
}
