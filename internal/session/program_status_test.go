package session

import "testing"

// TestProgramStatusAfterHookEnd checks that an auto-detected pane accepts idle
// from its program after a hook reports none, while an active hook keeps priority.
func TestProgramStatusAfterHookEnd(t *testing.T) {
	for _, state := range []AgentState{AgentStateNone, AgentStateWorking} {
		t.Run(state.Name(), func(t *testing.T) {
			sess, id := bareSessionWithWindow(t)
			ptyID := ptyIDOfWindow(t, sess, id)
			agent := newAgentMatcher(nil)
			running := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "codex", argv: []string{"codex"}}, true}})
			sess.applyAgentDetection(running, agent.identifyDetail)
			if _, applied, err := sess.ApplyAgentReport(id, AgentReport{State: state, Source: AgentSourceReport, Harness: "codex"}); err != nil || !applied {
				t.Fatalf("hook report: applied=%v, err=%v", applied, err)
			}
			sess.yieldNoneClaim(id)
			_, applied, err := sess.ApplyAgentReport(id, AgentReport{State: AgentStateIdle, Source: AgentSourceProgram})
			// When a program reports "idle", it should override "none" set by hook,
			// but it should keep "working" set by hook.
			if err != nil || applied != (state == AgentStateNone) {
				t.Fatalf("program report: applied=%v, err=%v", applied, err)
			}
			if state == AgentStateNone {
				sess.applyAgentDetection(running, agent.identifyDetail)
				if got := agentHarnessIDOf(t, sess, id); got != "codex" {
					t.Fatalf("harness = %q, want codex", got)
				}
				if got := agentStateOf(t, sess, id); got != AgentStateIdle {
					t.Fatalf("state = %q, want idle", got)
				}
			}
		})
	}
}
