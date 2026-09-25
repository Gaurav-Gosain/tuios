package session

import (
	"strings"
	"testing"
	"time"
)

// backdateTurn moves the start of a window's working phase into the past,
// standing in for the seconds a turn takes.
func backdateTurn(t *testing.T, sess *Session, windowID string, by time.Duration) {
	t.Helper()
	sess.stateMu.Lock()
	defer sess.stateMu.Unlock()
	turn, ok := sess.agentTurns[windowID]
	if !ok {
		t.Fatalf("window %s has no turn in progress", windowID)
	}
	turn.since = time.Now().Add(-by).UnixNano()
	sess.agentTurns[windowID] = turn
}

func completionSeqOf(t *testing.T, sess *Session, windowID string) uint64 {
	t.Helper()
	return windowStateOf(t, sess, windowID).CompletionSeq
}

func report(t *testing.T, sess *Session, windowID string, state AgentState) {
	t.Helper()
	if _, _, err := sess.ApplyAgentReport(windowID, AgentReport{State: state}); err != nil {
		t.Fatalf("ApplyAgentReport(%s): %v", state, err)
	}
}

// TestFinishedTurnsAreCounted covers the transitions that count and the ones
// that do not.
func TestFinishedTurnsAreCounted(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps func(t *testing.T, sess *Session, id string)
		want  uint64
	}{
		{"working to idle after a real turn", func(t *testing.T, sess *Session, id string) {
			report(t, sess, id, AgentStateWorking)
			backdateTurn(t, sess, id, agentMinTurn+time.Second)
			report(t, sess, id, AgentStateIdle)
		}, 1},
		{"working to unknown after a real turn", func(t *testing.T, sess *Session, id string) {
			report(t, sess, id, AgentStateWorking)
			backdateTurn(t, sess, id, agentMinTurn+time.Second)
			report(t, sess, id, AgentStateUnknown)
		}, 1},
		{"a blip of working does not count", func(t *testing.T, sess *Session, id string) {
			report(t, sess, id, AgentStateWorking)
			report(t, sess, id, AgentStateIdle)
		}, 0},
		{"an explicit done counts however short", func(t *testing.T, sess *Session, id string) {
			report(t, sess, id, AgentStateWorking)
			report(t, sess, id, AgentStateDone)
		}, 1},
		{"errored is not a finished turn", func(t *testing.T, sess *Session, id string) {
			report(t, sess, id, AgentStateWorking)
			backdateTurn(t, sess, id, agentMinTurn+time.Second)
			report(t, sess, id, AgentStateErrored)
		}, 0},
		{"a question in the middle keeps the turn", func(t *testing.T, sess *Session, id string) {
			report(t, sess, id, AgentStateWorking)
			backdateTurn(t, sess, id, agentMinTurn+time.Second)
			report(t, sess, id, AgentStateNeedsInput)
			report(t, sess, id, AgentStateWorking)
			report(t, sess, id, AgentStateIdle)
		}, 1},
		{"idle from nothing is not a turn", func(t *testing.T, sess *Session, id string) {
			report(t, sess, id, AgentStateIdle)
			report(t, sess, id, AgentStateDone)
		}, 0},
		{"two turns count twice", func(t *testing.T, sess *Session, id string) {
			for range 2 {
				report(t, sess, id, AgentStateWorking)
				backdateTurn(t, sess, id, agentMinTurn+time.Second)
				report(t, sess, id, AgentStateIdle)
			}
		}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess, id := bareSessionWithWindow(t)
			tc.steps(t, sess, id)
			if got := completionSeqOf(t, sess, id); got != tc.want {
				t.Fatalf("completion_seq = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestStallCountsTheTurnToItsLastOutput checks the silence timer measures a
// turn to when the pane went quiet. A redraw that bumped a detected pane to
// working for an instant is not a turn, although the timer only notices it
// thirty seconds later.
func TestStallCountsTheTurnToItsLastOutput(t *testing.T) {
	const stall = 30 * time.Second
	for _, tc := range []struct {
		name   string
		worked time.Duration
		want   uint64
	}{
		{"a redraw", 0, 0},
		{"a real turn", 10 * time.Second, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess, id := bareSessionWithWindow(t)
			report(t, sess, id, AgentStateWorking)
			start := time.Now().Add(-time.Minute)
			backdateTurn(t, sess, id, time.Minute)
			backdateAgentClaim(t, sess, id, time.Minute)
			lastOut := start.Add(tc.worked).UnixNano()
			sess.applyStallHeuristic(time.Now(), stall, func(string) int64 { return lastOut }, nil)
			if got := agentStateOf(t, sess, id); got != AgentStateIdle {
				t.Fatalf("state = %q, want idle", got)
			}
			if got := completionSeqOf(t, sess, id); got != tc.want {
				t.Fatalf("completion_seq = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestAPushFromAPaneDoesNotMarkATurnSeen: finished_unread is whether the
// person has looked, so a client running inside a pane, which is an agent
// looking, does not clear it. handleUpdateState passes mayActAsHuman as seen.
func TestAPushFromAPaneDoesNotMarkATurnSeen(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	push := sess.GetState()
	push.FocusedWindowID = ""
	sess.UpdateState(push)
	report(t, sess, id, AgentStateWorking)
	report(t, sess, id, AgentStateDone)

	push = sess.GetState()
	push.FocusedWindowID = id
	sess.UpdateStateFrom(push, false)
	w := windowStateOf(t, sess, id)
	if !sess.finishedUnread(&w) {
		t.Fatal("a push that is not the person's marked the finished turn seen")
	}
	sess.UpdateStateFrom(push, true)
	w = windowStateOf(t, sess, id)
	if sess.finishedUnread(&w) {
		t.Fatal("the person's push did not mark the finished turn seen")
	}
}

// TestListAgentsReportsFinishedTurns checks the verb's new fields, and that
// ready no longer covers unknown.
func TestListAgentsReportsFinishedTurns(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, a, b := twoWindowSession(t, d, "turns")
	report(t, sess, a, AgentStateWorking)
	report(t, sess, a, AgentStateDone)
	reportAs(t, sess, b, AgentStateUnknown, "claude-code")

	c := dialVerb(t, sp)
	res := result(t, c.call(t, `{"id":1,"verb":"list-agents","params":{"session":"turns"}}`))
	rows := map[string]map[string]any{}
	for _, r := range res["agents"].([]any) {
		m := r.(map[string]any)
		rows[m["window_id"].(string)] = m
	}
	if rows[a]["completion_seq"] != float64(1) {
		t.Errorf("completion_seq = %v, want 1", rows[a]["completion_seq"])
	}
	if _, ok := rows[a]["finished_unread"].(bool); !ok {
		t.Errorf("finished_unread missing: %v", rows[a])
	}
	if rows[b]["ready"] != false {
		t.Errorf("an unknown pane reads ready = %v, want false", rows[b]["ready"])
	}
}

// reportAs is report with the harness the report names.
func reportAs(t *testing.T, sess *Session, windowID string, state AgentState, harness string) {
	t.Helper()
	if _, _, err := sess.ApplyAgentReport(windowID, AgentReport{State: state, Harness: harness}); err != nil {
		t.Fatalf("ApplyAgentReport(%s, %s): %v", state, harness, err)
	}
}

// TestUnknownIsNotReady checks ask-agent and fan both refuse to type at a pane
// whose state is unknown when its harness can show that it is at its prompt,
// and that ask says why.
func TestUnknownIsNotReady(t *testing.T) {
	if agentRestStates[AgentStateUnknown.Name()] {
		t.Error("ask-agent treats unknown as ready")
	}
	if fanReadyStates[AgentStateUnknown.Name()] {
		t.Error("fan treats unknown as ready")
	}

	d, sp := startTestDaemon(t)
	sess, a, b := twoWindowSession(t, d, "quiet")
	reportAs(t, sess, b, AgentStateUnknown, "claude-code")
	st := sess.GetState()
	i, _ := findWindowStateIndex(st.Windows, b)
	if d.agentReady(st.Windows[i], fanReadyStates) {
		t.Error("fan treats an unknown claude-code pane as ready")
	}
	c := dialVerb(t, sp)
	resp := c.call(t, `{"id":1,"verb":"ask-agent","params":{"session":"quiet","window":"`+b+`","from":"`+a+`","text":"hello","ready_timeout":250}}`)
	if code := errCode(t, resp); code != ErrVerbNotReady {
		t.Fatalf("code = %q, want %q", code, ErrVerbNotReady)
	}
	e := resp["error"].(map[string]any)
	if !strings.Contains(e["message"].(string), "unknown") {
		t.Errorf("refusal did not say the state was unknown: %v", e["message"])
	}
}

// TestUnknownIsReadyWhenTheHarnessCannotProveIdle checks the other side: a
// harness with no idle rule can never show that it is at its prompt, so for it
// unknown is ready to fan, ask-agent and list-agents, as it was before idle
// rules existed. Without this, fan left the prompt unsent for every such
// harness.
func TestUnknownIsReadyWhenTheHarnessCannotProveIdle(t *testing.T) {
	d, sp := startTestDaemon(t)
	if d.agentMatcher.registry == nil || d.agentMatcher.registry.CanProveIdle("aider") {
		t.Skip("aider now has an idle rule; pick a harness without one")
	}
	if !d.agentMatcher.registry.CanProveIdle("claude-code") {
		t.Fatal("claude-code has no idle rule, so the other half of this contract is untested")
	}
	sess, _, b := twoWindowSession(t, d, "plain")
	reportAs(t, sess, b, AgentStateUnknown, "aider")
	st := sess.GetState()
	i, _ := findWindowStateIndex(st.Windows, b)
	if !d.agentReady(st.Windows[i], fanReadyStates) {
		t.Error("fan never types at an unknown aider pane")
	}
	c := dialVerb(t, sp)
	res := result(t, c.call(t, `{"id":1,"verb":"list-agents","params":{"session":"plain"}}`))
	for _, r := range res["agents"].([]any) {
		m := r.(map[string]any)
		if m["window_id"] == b && m["ready"] != true {
			t.Errorf("an unknown aider pane reads ready = %v, want true", m["ready"])
		}
	}
}
