package session

import (
	"testing"
)

// TestSubscribeReceivesAgentStateEvent verifies a write to agent state raises
// one agent-state event through the lifecycle diff, carrying the state's wire
// spelling, and that an unchanged state does not emit again.
func TestSubscribeReceivesAgentStateEvent(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")

	c := dialVerb(t, sp)
	result(t, c.call(t, `{"id":1,"verb":"subscribe","params":{"session":"work","types":["agent-state"]}}`))

	if err := sess.SetDaemonWindowAgentState("Window", AgentStateWorking, ""); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}
	ev := c.readResp(t)
	if ev["type"] != EventAgentState || ev["state"] != "working" {
		t.Fatalf("event = %v, want agent-state working", ev)
	}
	if ev["window"] == "" || ev["window"] == nil {
		t.Fatalf("agent-state event names no window: %v", ev)
	}

	// The same state again is not a change and must not emit; the next event on
	// the stream has to be the next actual transition.
	if err := sess.SetDaemonWindowAgentState("Window", AgentStateWorking, ""); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}
	if err := sess.SetDaemonWindowAgentState("Window", AgentStateIdle, ""); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}
	ev = c.readResp(t)
	if ev["type"] != EventAgentState || ev["state"] != "idle" {
		t.Fatalf("event after repeat = %v, want agent-state idle", ev)
	}
}

// TestWaitForAgentStateAlreadyTrue verifies a pane already sitting in the
// wanted state resolves the wait immediately: the prompt most worth alerting on
// is the one painted before anyone started waiting.
func TestWaitForAgentStateAlreadyTrue(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	if err := sess.SetDaemonWindowAgentState("Window", AgentStateNeedsInput, ""); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}

	c := dialVerb(t, sp)
	res := result(t, c.call(t, `{"id":1,"verb":"wait-for","params":{"condition":"agent-state","session":"work","until":"needs_input","timeout":8000}}`))
	if res["matched"] != true || res["state"] != "needs_input" {
		t.Fatalf("wait result = %v, want matched needs_input", res)
	}
}
