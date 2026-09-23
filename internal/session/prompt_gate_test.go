package session

import (
	"strings"
	"testing"
	"time"
)

// silenceWindow turns a pane's shell into one that reads its input and prints
// nothing: echo off, then cat into /dev/null. It is the pane an agent looks like
// when it dropped the prompt, and the one the stall gate is for.
func silenceWindow(t *testing.T, d *Daemon, sess *Session, windowID string) *PTY {
	t.Helper()
	pty, err := d.resolvePTYForTarget(sess, windowID)
	if err != nil {
		t.Fatalf("resolvePTYForTarget: %v", err)
	}
	waitForQuiet(t, pty, 300*time.Millisecond, 5*time.Second)
	written := time.Now().UnixNano()
	if _, err := pty.Write([]byte("stty -echo; exec cat >/dev/null\r")); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Wait for the shell to have echoed the command, so the quiet that follows
	// is after it ran and not the prompt's from before.
	deadline := time.Now().Add(5 * time.Second)
	for pty.LastOutput() <= written || !strings.Contains(pty.CaptureContent(true, false), "/dev/null") {
		if time.Now().After(deadline) {
			t.Fatal("the shell did not echo the command that silences it")
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

	// No harness: output after Enter is the only evidence a plain pane gives.
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

	// A harness that can show working: output alone is not evidence, because a
	// TUI that read Enter as a newline redraws too.
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

	// A turn that finished between two looks still counts: the pane is back
	// at idle, and CompletionSeq says a turn ended.
	report(AgentReport{State: AgentStateIdle, Harness: "claude-code"})
	g = d.newPromptGate(sess, b)
	g.markSubmitted(now)
	report(AgentReport{State: AgentStateWorking, Harness: "claude-code"})
	report(AgentReport{State: AgentStateDone, Harness: "claude-code"})
	report(AgentReport{State: AgentStateIdle, Harness: "claude-code"})
	if !g.check(sess, nil) || g.taken != "turn" {
		t.Errorf("a turn that came and went was not seen: taken %q", g.taken)
	}

	// needs_input counts only when the pane was not already on it.
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

	// The question was typed, so the record of the ask is kept.
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

// TestFanRecordsAStalledPrompt covers the same check on fan's delivery: a
// prompt typed at an agent that did not take it is stalled, not sent.
func TestFanRecordsAStalledPrompt(t *testing.T) {
	d, _ := startTestDaemon(t)
	d.promptStallOverride = 800 * time.Millisecond

	for _, tc := range []struct {
		name   string
		silent bool
		want   string
	}{
		{"silent pane", true, PromptStalled},
		{"echoing pane", false, PromptSent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess := makeSessionWithWindow(t, d, "fan-"+strings.ReplaceAll(tc.name, " ", "-"))
			if err := sess.SetWorktree(&WorktreeInfo{Group: "g", Prompt: "hello", PromptStatus: PromptPending}); err != nil {
				t.Fatalf("SetWorktree: %v", err)
			}
			w := sess.GetState().Windows[0].ID
			if tc.silent {
				silenceWindow(t, d, sess, w)
			}
			if _, _, err := sess.ApplyAgentReport(w, AgentReport{State: AgentStateIdle}); err != nil {
				t.Fatalf("ApplyAgentReport: %v", err)
			}
			d.deliverFanPrompt(sess, w, "", "echo tuios_fan_marker", 5*time.Second)
			info := sess.Worktree()
			if info.PromptStatus != tc.want {
				t.Fatalf("prompt_status = %q (%s), want %q", info.PromptStatus, info.PromptNote, tc.want)
			}
			if tc.want == PromptStalled && !strings.Contains(info.PromptNote, "Enter was sent") {
				t.Errorf("the note does not say the prompt was typed: %q", info.PromptNote)
			}
		})
	}
}
