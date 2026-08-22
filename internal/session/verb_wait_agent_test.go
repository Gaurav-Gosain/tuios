package session

import (
	"testing"
	"time"
)

// TestWaitForAgentStateTransition verifies the agent-state condition resolves
// when a window's agent state reaches one of the states named in until, without
// naming a window: "any agent in this session needs input" is the shape hooks
// and scripts want, and it was previously only expressible as a poll loop.
func TestWaitForAgentStateTransition(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")

	c := dialVerb(t, sp)
	done := make(chan map[string]any, 1)
	go func() {
		done <- c.call(t, `{"id":1,"verb":"wait-for","params":{"condition":"agent-state","session":"work","until":"needs_input,errored","timeout":8000}}`)
	}()

	time.Sleep(150 * time.Millisecond)
	// working first: a state until does not name must not resolve the wait.
	if err := sess.SetDaemonWindowAgentState("Window", AgentStateWorking, ""); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	select {
	case resp := <-done:
		t.Fatalf("wait resolved on working: %v", resp)
	default:
	}
	if err := sess.SetDaemonWindowAgentState("Window", AgentStateNeedsInput, "pick an option"); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}

	select {
	case resp := <-done:
		res := result(t, resp)
		if res["matched"] != true || res["state"] != "needs_input" {
			t.Fatalf("wait result = %v, want matched needs_input", res)
		}
		if res["window"] == "" || res["window"] == nil {
			t.Fatalf("wait result names no window: %v", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("wait-for agent-state did not resolve")
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

// TestWaitForAgentStateRejectsUnknownUntil verifies the until parameter is
// validated with the accepted states in the hint, not silently never-matching.
func TestWaitForAgentStateRejectsUnknownUntil(t *testing.T) {
	_, sp := startTestDaemon(t)

	c := dialVerb(t, sp)
	resp := c.call(t, `{"id":1,"verb":"wait-for","params":{"condition":"agent-state","session":"work","until":"blocked","timeout":500}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Fatalf("error code = %q, want %q", code, ErrVerbInvalidParams)
	}
}

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
