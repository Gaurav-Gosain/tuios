//go:build !slim

package session

import (
	"sync/atomic"
	"testing"
	"time"
)

// detectionVerbs are the three verbs that report where a pane's agent state
// came from, each called on one window.
var detectionVerbs = []string{"get-agent-state", "explain-agent-detect", "list-agents"}

// detectionRow calls one detection verb for a window and returns the part of
// the answer that describes it: the result itself, or its list-agents row.
func detectionRow(t *testing.T, sp string, verb, session, windowID string) map[string]any {
	t.Helper()
	c := dialVerb(t, sp)
	params := `{"session":"` + session + `","window":"` + windowID + `"}`
	if verb == "list-agents" {
		params = `{"session":"` + session + `","all":true}`
	}
	res := result(t, c.call(t, `{"id":1,"verb":"`+verb+`","params":`+params+`}`))
	if verb != "list-agents" {
		return res
	}
	rows, _ := res["agents"].([]any)
	for _, r := range rows {
		row, _ := r.(map[string]any)
		if row["window_id"] == windowID {
			return row
		}
	}
	t.Fatalf("list-agents has no row for %s: %v", windowID, res)
	return nil
}

// TestEvidenceAgeUnderAFakeClock pins evidence_age_ms on the three detection
// verbs: it is the time since the state was last stamped, read against the
// daemon's clock, it grows while nothing new arrives, and a pane nothing ever
// set a state on has no age at all.
func TestEvidenceAgeUnderAFakeClock(t *testing.T) {
	t.Setenv("TUIOS_AGENT_DETECT_SECONDS", "0")
	d, sp := startTestDaemon(t)
	var now atomic.Int64
	d.evidenceClock = func() time.Time { return time.Unix(0, now.Load()) }

	sess := makeSessionWithWindow(t, d, "age")
	winID := sess.GetState().Windows[0].ID

	now.Store(time.Now().UnixNano())
	for _, verb := range detectionVerbs {
		if age, present := detectionRow(t, sp, verb, "age", winID)["evidence_age_ms"]; !present || age != nil {
			t.Errorf("%s: a pane with no state has evidence_age_ms %v (present %v), want null", verb, age, present)
		}
	}

	if _, _, err := sess.ApplyAgentReport(winID, AgentReport{State: AgentStateUnknown}); err != nil {
		t.Fatal(err)
	}
	var stampedAt int64
	for _, w := range sess.GetState().Windows {
		if w.ID == winID {
			stampedAt = w.AgentStateAt
		}
	}
	if stampedAt == 0 {
		t.Fatal("the report stamped no AgentStateAt")
	}

	for _, elapsed := range []time.Duration{1500 * time.Millisecond, 4 * time.Second} {
		now.Store(stampedAt + int64(elapsed))
		for _, verb := range detectionVerbs {
			row := detectionRow(t, sp, verb, "age", winID)
			if got, want := row["evidence_age_ms"], float64(elapsed.Milliseconds()); got != want {
				t.Errorf("%s after %v: evidence_age_ms = %v, want %v", verb, elapsed, got, want)
			}
		}
	}

	// A clock behind the stamp, which a merged state from a faster machine can
	// cause, reads as fresh rather than negative.
	now.Store(stampedAt - int64(time.Second))
	if got := detectionRow(t, sp, "get-agent-state", "age", winID)["evidence_age_ms"]; got != float64(0) {
		t.Errorf("a stamp in the future reads evidence_age_ms %v, want 0", got)
	}
}

// TestListAgentsNamesAHintedPaneHint pins the identity field on list-agents
// for the weakest tier: a pane whose process is not an agent and whose only
// evidence is TUIOS_AGENT in its environment reads identity hint, the same
// word get-agent-state uses.
func TestListAgentsNamesAHintedPaneHint(t *testing.T) {
	t.Setenv("TUIOS_AGENT_DETECT_SECONDS", "0")
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "hinted")
	winID := sess.GetState().Windows[0].ID
	ptyID := ptyIDOfWindow(t, sess, winID)

	wrapper := foregroundInfo{
		comm: "docker", argv: []string{"docker", "run", "-it", "box"}, pid: 42,
		hint: func() string { return "claude-code" },
	}
	resolve := fakeResolver(map[string]fakeProc{ptyID: {wrapper, true}})
	if n := sess.applyAgentDetection(resolve, d.agentMatcher.identifyDetail); n != 1 {
		t.Fatalf("detection changed %d windows, want 1", n)
	}

	for _, verb := range []string{"list-agents", "get-agent-state"} {
		row := detectionRow(t, sp, verb, "hinted", winID)
		if row["identity"] != "hint" || row["confidence"] != "strong" {
			t.Errorf("%s: identity %v confidence %v, want hint strong", verb, row["identity"], row["confidence"])
		}
	}
}

// TestEvidenceAgeFollowsOutputForAnInferredState: a pane the detector holds at
// working, which keeps printing, reads fresh. The detector's state is inferred
// from the pane, and output is the evidence the silence timer counts too, so
// the age runs from the later of the stamp and the last output. A state the
// agent reported itself keeps its own stamp, whatever the pane prints.
func TestEvidenceAgeFollowsOutputForAnInferredState(t *testing.T) {
	t.Setenv("TUIOS_AGENT_DETECT_SECONDS", "0")
	d, sp := startTestDaemon(t)
	var now atomic.Int64
	d.evidenceClock = func() time.Time { return time.Unix(0, now.Load()) }
	sess := makeSessionWithWindow(t, d, "printing")
	winID := sess.GetState().Windows[0].ID
	ptyID := ptyIDOfWindow(t, sess, winID)
	pty := sess.GetPTY(ptyID)
	if pty == nil {
		t.Fatal("the window has no pty")
	}
	// The pane's real shell prints its prompt once. Wait for that to settle,
	// or its write lands on top of the output times the test sets below.
	deadline := time.Now().Add(5 * time.Second)
	for last := int64(-1); ; {
		time.Sleep(300 * time.Millisecond)
		cur := pty.LastOutput()
		if cur != 0 && cur == last {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the pane's shell never went quiet")
		}
		last = cur
	}

	resolve := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "claude", argv: []string{"claude"}}, true}})
	if n := sess.applyAgentDetection(resolve, d.agentMatcher.identifyDetail); n != 1 {
		t.Fatalf("detection changed %d windows, want 1", n)
	}
	stampOf := func() int64 {
		for _, w := range sess.GetState().Windows {
			if w.ID == winID {
				return w.AgentStateAt
			}
		}
		return 0
	}
	stampedAt := stampOf()
	if claim := sess.agentClaimFor(winID); claim.source != AgentSourceDetect || stampedAt == 0 {
		t.Fatalf("detection left source %q, stamp %d; want detect and a stamp", claim.source, stampedAt)
	}

	// The pane prints every 300 ms for 45 s after the detector saw it.
	for _, elapsed := range []time.Duration{20 * time.Second, 35 * time.Second, 45 * time.Second} {
		at := stampedAt + int64(elapsed)
		pty.lastOutput.Store(at - int64(300*time.Millisecond))
		now.Store(at)
		for _, verb := range detectionVerbs {
			if got := detectionRow(t, sp, verb, "printing", winID)["evidence_age_ms"]; got != float64(300) {
				t.Errorf("%s %v after detection, output 300 ms ago: evidence_age_ms = %v, want 300", verb, elapsed, got)
			}
		}
	}

	// The agent reports for itself. Its report is the evidence now, and the
	// pane's output does not refresh it.
	if _, _, err := sess.ApplyAgentReport(winID, AgentReport{State: AgentStateWorking}); err != nil {
		t.Fatal(err)
	}
	reportedAt := stampOf()
	pty.lastOutput.Store(reportedAt + int64(9*time.Second))
	now.Store(reportedAt + int64(10*time.Second))
	if got := detectionRow(t, sp, "get-agent-state", "printing", winID)["evidence_age_ms"]; got != float64(10000) {
		t.Errorf("a reported state reads evidence_age_ms %v, want 10000 from the report", got)
	}
}
