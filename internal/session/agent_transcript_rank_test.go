package session

import "testing"

// newTestSessionWithWindow gives the ranking tests one real window to claim,
// without a daemon: precedence is decided entirely inside the session.
func newTestSessionWithWindow(t *testing.T) *Session {
	t.Helper()
	sess := newTestSession(t)
	if _, err := sess.AddDaemonWindow("shell", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	return sess
}

// The transcript tier sits between the harness reporting for itself and the
// escape sequences it emits. Below report because of latency, not trust:
// records land at message boundaries, so a hook is speaking about now while the
// file can be a turn behind.
func TestTranscriptRanksBetweenReportAndOSC(t *testing.T) {
	if !(AgentSourceReport.rank() > AgentSourceTranscript.rank()) {
		t.Fatalf("transcript (%d) must rank below report (%d)",
			AgentSourceTranscript.rank(), AgentSourceReport.rank())
	}
	if !(AgentSourceTranscript.rank() > AgentSourceOSC.rank()) {
		t.Fatalf("transcript (%d) must rank above osc (%d)",
			AgentSourceTranscript.rank(), AgentSourceOSC.rank())
	}
}

// It is worked out by the daemon looking at the machine, so a caller cannot
// claim it over the socket, exactly as with the process detector.
func TestTranscriptIsNotAcceptedFromACaller(t *testing.T) {
	if _, ok := ParseAgentSource("transcript"); ok {
		t.Fatal("set-agent-state must not accept source=transcript")
	}
	for _, n := range AgentSourceNames {
		if n == "transcript" {
			t.Fatal("transcript must not be advertised as an accepted source")
		}
	}
	// It is still reportable outward, so a user can see which tier answered.
	if AgentSourceTranscript.Name() != "transcript" {
		t.Fatalf("Name() = %q", AgentSourceTranscript.Name())
	}
}

func TestTranscriptOutranksTheWeakerTiers(t *testing.T) {
	s := newTestSessionWithWindow(t)
	win := s.GetState().Windows[0].ID

	if _, applied, err := s.ApplyAgentReport(win, AgentReport{
		State: AgentStateWorking, Source: AgentSourceTranscript, Harness: "claude-code",
	}); err != nil || !applied {
		t.Fatalf("transcript report: %v applied=%v", err, applied)
	}
	// A weaker tier guessing at the same pane leaves the better answer alone.
	for _, weaker := range []AgentSource{AgentSourceOSC, AgentSourceScreen, AgentSourceStall} {
		if _, applied, err := s.ApplyAgentReport(win, AgentReport{
			State: AgentStateIdle, Source: weaker,
		}); err != nil || applied {
			t.Fatalf("%s overwrote the transcript: applied=%v err=%v", weaker, applied, err)
		}
	}
	// The harness reporting for itself still wins, because it is speaking now.
	if _, applied, err := s.ApplyAgentReport(win, AgentReport{
		State: AgentStateNeedsInput, Source: AgentSourceReport,
	}); err != nil || !applied {
		t.Fatalf("report over transcript: %v applied=%v", err, applied)
	}
}

// The yield is the answer to a claim that outlives the thing that made it. An
// agent that dies without a final record would otherwise hold its pane on
// whatever it last said, against every weaker tier, forever.
func TestYieldingAClaimReopensTheWindowWithoutChangingIt(t *testing.T) {
	s := newTestSessionWithWindow(t)
	win := s.GetState().Windows[0].ID

	if _, applied, _ := s.ApplyAgentReport(win, AgentReport{
		State: AgentStateWorking, Source: AgentSourceTranscript,
	}); !applied {
		t.Fatal("transcript claim not applied")
	}
	if !s.yieldAgentClaim(win, AgentSourceTranscript) {
		t.Fatal("yield reported nothing to give back")
	}
	// What was last read is still the best answer anyone has; it just stops
	// being defended.
	if got := s.GetState().Windows[0].AgentState; got != AgentStateWorking {
		t.Fatalf("state after yield = %q, want it left alone", got)
	}
	// And now the weakest tier can speak.
	if _, applied, err := s.ApplyAgentReport(win, AgentReport{
		State: AgentStateIdle, Source: AgentSourceStall,
	}); err != nil || !applied {
		t.Fatalf("stall after yield: %v applied=%v", err, applied)
	}
}

// Yielding a window the process detector owns hands it back to the detector, not
// to the zero claim: an empty source ranks as a report, so returning it that way
// would pin the pane at the highest rank instead of freeing it.
func TestYieldingKeepsTheDetectorsLifecycleClaim(t *testing.T) {
	s := newTestSessionWithWindow(t)
	win := s.GetState().Windows[0].ID
	s.setAgentClaim(win, agentClaim{source: AgentSourceTranscript, auto: true})

	if !s.yieldAgentClaim(win, AgentSourceTranscript) {
		t.Fatal("yield reported nothing to give back")
	}
	claim := s.agentClaimFor(win)
	if !claim.auto {
		t.Fatal("the detector's lifecycle claim was dropped, so the pane will never clear")
	}
	if claim.source != AgentSourceDetect {
		t.Fatalf("source after yield = %q, want detect", claim.source)
	}
	// The pane is back exactly where it was before the transcript spoke: under
	// the detector, and open to every tier above it. The silence timer is not
	// checked here because it does not come through this gate at all; it reads
	// working and writes idle directly, which is already narrower.
	if _, applied, err := s.ApplyAgentReport(win, AgentReport{
		State: AgentStateNeedsInput, Source: AgentSourceScreen,
	}); err != nil || !applied {
		t.Fatalf("screen after yield: %v applied=%v", err, applied)
	}
}

// Only the source that made a claim may yield it, so a stale reader cannot
// unseat whoever took the pane over in the meantime.
func TestYieldOnlyDropsYourOwnClaim(t *testing.T) {
	s := newTestSessionWithWindow(t)
	win := s.GetState().Windows[0].ID
	if _, applied, _ := s.ApplyAgentReport(win, AgentReport{
		State: AgentStateNeedsInput, Source: AgentSourceReport,
	}); !applied {
		t.Fatal("report not applied")
	}
	if s.yieldAgentClaim(win, AgentSourceTranscript) {
		t.Fatal("transcript yielded a claim it did not hold")
	}
	if s.agentClaimFor(win).source != AgentSourceReport {
		t.Fatal("the report's claim was disturbed")
	}
}
