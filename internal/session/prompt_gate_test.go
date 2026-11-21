package session

import (
	"strings"
	"testing"
	"time"
)

func silenceWindow(t *testing.T, d *Daemon, sess *Session, windowID string) *PTY {
	t.Helper()
	pty, err := d.resolvePTYForTarget(sess, windowID)
	if err != nil {
		t.Fatalf("resolvePTYForTarget: %v", err)
	}
	waitForQuiet(t, pty, 300*time.Millisecond, 5*time.Second)
	if _, err := pty.Write([]byte("stty -echo; printf '%s%s\\n' SIL ENCED; exec cat >/dev/null\r")); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(pty.CaptureContent(true, false), "SILENCED") {
		if time.Now().After(deadline) {
			t.Fatal("the shell did not run the command that silences it")
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitForQuiet(t, pty, 300*time.Millisecond, 5*time.Second)
	return pty
}

// TestPromptGateEvidence pins what counts as a pane having taken a prompt, for
// a pane with no harness and for one whose harness can show working.
func TestPromptGateEvidence(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess, _, b := twoWindowSession(t, d, "gate")
	report := func(r AgentReport) {
		t.Helper()
		if _, _, err := sess.ApplyAgentReport(b, r); err != nil {
			t.Fatalf("ApplyAgentReport: %v", err)
		}
	}
	now := time.Now()
	before := func() int64 { return now.Add(-time.Second).UnixNano() }
	after := func() int64 { return now.Add(time.Second).UnixNano() }

	report(AgentReport{State: AgentStateIdle})
	g := d.newPromptGate(sess, b)
	g.markSubmitted(now)
	if !g.outputCounts {
		t.Fatal("output does not count for a pane with no harness")
	}
	if g.check(sess, before) {
		t.Error("output from before Enter counted as the prompt being taken")
	}
	if !g.check(sess, after) || g.taken != "output" {
		t.Errorf("output after Enter did not count: taken %q", g.taken)
	}

	report(AgentReport{State: AgentStateIdle, Harness: "claude-code"})
	g = d.newPromptGate(sess, b)
	g.markSubmitted(now)
	if g.outputCounts {
		t.Fatal("output counts for claude-code, whose rules show working")
	}
	if g.check(sess, after) {
		t.Error("output alone counted for a harness that can show working")
	}
	report(AgentReport{State: AgentStateWorking, Harness: "claude-code"})
	if !g.check(sess, after) || g.taken != "working" {
		t.Errorf("working did not count: taken %q", g.taken)
	}

	report(AgentReport{State: AgentStateIdle, Harness: "claude-code"})
	g = d.newPromptGate(sess, b)
	g.markSubmitted(now)
	report(AgentReport{State: AgentStateWorking, Harness: "claude-code"})
	report(AgentReport{State: AgentStateDone, Harness: "claude-code"})
	report(AgentReport{State: AgentStateIdle, Harness: "claude-code"})
	if !g.check(sess, nil) || g.taken != "turn" {
		t.Errorf("a turn that came and went was not seen: taken %q", g.taken)
	}

	report(AgentReport{State: AgentStateNeedsInput, Harness: "claude-code", Kind: "question"})
	g = d.newPromptGate(sess, b)
	g.markSubmitted(now)
	if g.check(sess, nil) {
		t.Error("a pane still on the needs_input it started on counted as taking the prompt")
	}
	g.observe(streamEvent{Type: EventAgentState, Window: b, State: "needs_input", Time: now.Add(time.Millisecond).UnixNano()})
	if g.taken != "" {
		t.Error("an event for the needs_input it started on counted")
	}
	g.observe(streamEvent{Type: EventAgentState, Window: b, State: "working", Time: now.Add(-time.Millisecond).UnixNano()})
	if g.taken != "" {
		t.Error("an event from before Enter counted")
	}
	g.observe(streamEvent{Type: EventAgentState, Window: b, State: "working", Time: now.Add(time.Millisecond).UnixNano()})
	if g.taken != "working" {
		t.Errorf("a working event after Enter did not count: taken %q", g.taken)
	}
}

// TestAskFailsWithPromptStalledOnASilentPane covers the herdr stall check: a
// pane that takes the question and shows nothing used to be answered with an
// empty reply as if the agent had nothing to say. Now the ask fails with
// prompt_stalled, says the text was typed, and points at capture-pane.
func TestAskFailsWithPromptStalledOnASilentPane(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, a, b := twoWindowSession(t, d, "stall")
	c := dialVerb(t, sp)
	silenceWindow(t, d, sess, b)

	start := time.Now()
	resp := c.call(t, `{"id":1,"verb":"ask-agent","params":{"session":"stall","window":"`+b+`","from":"`+a+`","text":"are you there","settle":300,"stall_timeout":800,"timeout":15000}}`)
	if code := errCode(t, resp); code != ErrVerbPromptStalled {
		t.Fatalf("code = %q, want %q: %v", code, ErrVerbPromptStalled, resp)
	}
	if waited := time.Since(start); waited < 800*time.Millisecond {
		t.Errorf("the ask stalled after %v, before the stall window ended", waited)
	}
	e := errorOf(t, resp)
	if msg, _ := e["message"].(string); !strings.Contains(msg, "Enter was sent") {
		t.Errorf("the message does not say the prompt was typed: %q", msg)
	}
	if hint, _ := e["hint"].(map[string]any); hint == nil || hint["verb"] != "capture-pane" {
		t.Errorf("the hint does not point at capture-pane: %v", e["hint"])
	}

	read := result(t, c.call(t, `{"id":2,"verb":"read-agent-messages","params":{"session":"stall"}}`))
	msgs, _ := read["messages"].([]any)
	found := false
	for _, m := range msgs {
		if mm := m.(map[string]any); mm["kind"] == agentMsgAsk && mm["subject"] == "are you there" {
			found = true
		}
	}
	if !found {
		t.Errorf("a stalled ask left no record: %v", msgs)
	}
}
