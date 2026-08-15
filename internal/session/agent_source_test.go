package session

import (
	"testing"
	"time"
)

// TestParseAgentSource checks the wire-name mapping and, most of all, that an
// omitted source is accepted and means report: that default is what keeps every
// caller written before this field existed working exactly as it did.
func TestParseAgentSource(t *testing.T) {
	cases := map[string]struct {
		want AgentSource
		ok   bool
	}{
		"":       {AgentSourceReport, true},
		"report": {AgentSourceReport, true},
		"osc":    {AgentSourceOSC, true},
		"screen": {AgentSourceScreen, true},
		"stall":  {AgentSourceStall, true},
		"bogus":  {"", false},
		// The detector's own source is daemon-internal, not something a caller
		// reports, so it is not accepted from the wire.
		"detect": {"", false},
	}
	for in, want := range cases {
		got, ok := ParseAgentSource(in)
		if ok != want.ok || (ok && got != want.want) {
			t.Errorf("ParseAgentSource(%q) = (%q, %v), want (%q, %v)", in, got, ok, want.want, want.ok)
		}
	}
	if AgentSource("").Name() != "report" {
		t.Errorf("unset source names itself %q, want report", AgentSource("").Name())
	}
}

// TestAgentSourceRanking pins the order the whole feature rests on. It is a
// table rather than a set of assertions on the numbers, because only the
// ordering is contractual.
func TestAgentSourceRanking(t *testing.T) {
	ordered := []AgentSource{AgentSourceStall, AgentSourceDetect, AgentSourceScreen, AgentSourceOSC, AgentSourceReport}
	for i := 1; i < len(ordered); i++ {
		if ordered[i-1].rank() >= ordered[i].rank() {
			t.Errorf("%s does not rank below %s", ordered[i-1], ordered[i])
		}
	}
	if AgentSource("").rank() != AgentSourceReport.rank() {
		t.Error("an unset source must rank as a report")
	}
}

// TestLowerSourceCannotOverwriteHigher is the point of replacing the ownership
// bool: a screen rule guessing at a pane whose harness reports for itself must
// lose, and a source updating its own claim must not.
func TestLowerSourceCannotOverwriteHigher(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"set-agent-state","params":{"session":"work","window":"Window","state":"needs_input","source":"report"}}`))
	if res["applied"] != true {
		t.Fatalf("report was not applied: %v", res)
	}

	held := sess.GetState().Version

	// A lower-ranked source loses, and is told so rather than getting an error.
	refused := result(t, c.call(t, `{"id":2,"verb":"set-agent-state","params":{"session":"work","window":"Window","state":"working","source":"screen"}}`))
	if refused["applied"] != false {
		t.Fatalf("a screen report overwrote a harness report: %v", refused)
	}
	if refused["state"] != "needs_input" {
		t.Fatalf("refused report returned state %v, want the state that stands (needs_input)", refused["state"])
	}
	if got := sess.GetState().Version; got != held {
		t.Fatalf("a refused report bumped the version: %d -> %d", held, got)
	}
	if st := agentStateOf(t, sess, windowIDOf(t, sess)); st != AgentStateNeedsInput {
		t.Fatalf("state after a refused report = %q, want needs_input", st)
	}

	// The same source updating its own claim is allowed.
	again := result(t, c.call(t, `{"id":3,"verb":"set-agent-state","params":{"session":"work","window":"Window","state":"done","source":"report"}}`))
	if again["applied"] != true || again["state"] != "done" {
		t.Fatalf("a source could not update its own claim: %v", again)
	}
}

// TestHigherSourceOverwritesLower is the other half: the ranking is an ordering,
// not a first-writer-wins lock.
func TestHigherSourceOverwritesLower(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	_ = result(t, c.call(t, `{"id":1,"verb":"set-agent-state","params":{"session":"work","window":"Window","state":"working","source":"screen"}}`))
	res := result(t, c.call(t, `{"id":2,"verb":"set-agent-state","params":{"session":"work","window":"Window","state":"needs_input","source":"osc"}}`))
	if res["applied"] != true {
		t.Fatalf("osc did not outrank screen: %v", res)
	}
	if st := agentStateOf(t, sess, windowIDOf(t, sess)); st != AgentStateNeedsInput {
		t.Fatalf("state = %q, want needs_input", st)
	}
}

// TestSetAgentStateWithoutSourceIsUnchanged is the compatibility case: the
// shipped hook shim and every other existing caller send no source, and must
// behave exactly as they did, which means outranking everything below report.
func TestSetAgentStateWithoutSourceIsUnchanged(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	// A screen rule got there first.
	_ = result(t, c.call(t, `{"id":1,"verb":"set-agent-state","params":{"session":"work","window":"Window","state":"working","source":"screen"}}`))

	// The pre-existing call shape, with no source at all.
	res := result(t, c.call(t, `{"id":2,"verb":"set-agent-state","params":{"session":"work","window":"Window","state":"needs_input","message":"awaiting approval"}}`))
	if res["applied"] != true {
		t.Fatalf("a sourceless report was refused: %v", res)
	}
	if res["state"] != "needs_input" || res["message"] != "awaiting approval" {
		t.Fatalf("a sourceless report changed shape: %v", res)
	}
	if res["source"] != "report" {
		t.Fatalf("a sourceless report recorded source %v, want report", res["source"])
	}
	if st := agentStateOf(t, sess, windowIDOf(t, sess)); st != AgentStateNeedsInput {
		t.Fatalf("state = %q, want needs_input", st)
	}
}

// TestGetAgentStateReportsSourceAndHarness checks the read side carries enough
// to explain a shown state, and that an untouched window reads as a report with
// no harness, which is what it looked like before sources existed.
func TestGetAgentStateReportsSourceAndHarness(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	fresh := result(t, c.call(t, `{"id":1,"verb":"get-agent-state","params":{"session":"work","window":"Window"}}`))
	if fresh["source"] != "report" || fresh["harness_id"] != "" {
		t.Fatalf("unclaimed window reads as %v, want source report and no harness", fresh)
	}

	_ = result(t, c.call(t, `{"id":2,"verb":"set-agent-state","params":{"session":"work","window":"Window","state":"working","source":"osc","harness":"claude-code"}}`))
	got := result(t, c.call(t, `{"id":3,"verb":"get-agent-state","params":{"session":"work","window":"Window"}}`))
	if got["source"] != "osc" {
		t.Fatalf("get-agent-state source = %v, want osc", got["source"])
	}
	if got["harness_id"] != "claude-code" {
		t.Fatalf("get-agent-state harness_id = %v, want claude-code", got["harness_id"])
	}
}

// TestSetAgentStateRejectsBadSource checks an unknown source is a parameter
// error with a usable hint, not a silent downgrade to report.
func TestSetAgentStateRejectsBadSource(t *testing.T) {
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	code := errCode(t, c.call(t, `{"id":1,"verb":"set-agent-state","params":{"session":"work","state":"working","source":"bogus"}}`))
	if code != ErrVerbInvalidParams {
		t.Fatalf("bad source gave code %q, want %q", code, ErrVerbInvalidParams)
	}
}

// TestReportOutranksTheDetector checks the detector's own claim is ranked like
// any other: an explicit report takes the state over, and the detector still
// clears the pane when the agent exits, because that is a lifecycle claim rather
// than a precedence one.
func TestReportOutranksTheDetector(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, id)
	agent := newAgentMatcher(nil)
	running := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "claude", argv: []string{"claude"}}, true}})

	if n := sess.applyAgentDetection(running, agent.identify); n != 1 {
		t.Fatalf("promotion changed %d windows, want 1", n)
	}
	if src := sess.agentClaimFor(id).source; src != AgentSourceDetect {
		t.Fatalf("detector claim source = %q, want detect", src)
	}

	// A stall report cannot take a pane the detector holds.
	if _, applied, err := sess.ApplyAgentReport(id, AgentReport{State: AgentStateIdle, Source: AgentSourceStall}); err != nil || applied {
		t.Fatalf("a stall report took a detector-held pane: applied=%v err=%v", applied, err)
	}

	// A report can.
	if _, applied, err := sess.ApplyAgentReport(id, AgentReport{State: AgentStateNeedsInput}); err != nil || !applied {
		t.Fatalf("a report could not take a detector-held pane: applied=%v err=%v", applied, err)
	}
	if src := sess.agentClaimFor(id).source; src != AgentSourceReport {
		t.Fatalf("claim source after a report = %q, want report", src)
	}

	// The detector still owns the pane's lifecycle and clears it on exit.
	shell := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "bash", argv: []string{"-bash"}}, true}})
	if n := sess.applyAgentDetection(shell, agent.identify); n != 1 {
		t.Fatalf("agent exit changed %d windows, want 1", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateNone {
		t.Fatalf("state after the agent exited = %q, want none", got)
	}
}

// TestStallHeuristicStillDemotesAReport pins the one deliberate exception to the
// ranking: the silence timer writes directly rather than through the gate, so a
// reported working state that goes quiet is still demoted, exactly as before.
// Gating it would have turned the heuristic off for the case it exists for.
func TestStallHeuristicStillDemotesAReport(t *testing.T) {
	sess, id := bareSessionWithWindow(t)

	if err := sess.SetDaemonWindowAgentState(id, AgentStateWorking, ""); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}
	now := time.Now()
	if n := sess.applyStallHeuristic(now.Add(time.Hour), time.Minute, func(string) int64 { return 0 }, nil); n != 1 {
		t.Fatalf("stall demoted %d windows, want 1", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateIdle {
		t.Fatalf("state after stall = %q, want idle", got)
	}
	// Having demoted it, the heuristic owns the state, so it says so.
	if src := sess.agentClaimFor(id).source; src != AgentSourceStall {
		t.Fatalf("claim source after stall = %q, want stall", src)
	}
}

// TestAgentClaimDroppedWithTheWindow checks a claim does not outlive its window,
// which is what keeps the map bounded now that any source can add to it.
func TestAgentClaimDroppedWithTheWindow(t *testing.T) {
	sess, id := bareSessionWithWindow(t)

	if err := sess.SetDaemonWindowAgentState(id, AgentStateWorking, ""); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}
	if sess.agentClaimFor(id).source == "" {
		t.Fatal("no claim recorded for a reported window")
	}
	if _, err := sess.CloseDaemonWindow(id); err != nil {
		t.Fatalf("CloseDaemonWindow: %v", err)
	}
	if claim := sess.agentClaimFor(id); claim.source != "" {
		t.Fatalf("claim survived its window: %+v", claim)
	}
}

// windowIDOf returns the only window's ID, for tests built on
// makeSessionWithWindow.
func windowIDOf(t *testing.T, sess *Session) string {
	t.Helper()
	state := sess.GetState()
	if len(state.Windows) != 1 {
		t.Fatalf("want exactly one window, got %d", len(state.Windows))
	}
	return state.Windows[0].ID
}
