package session

import (
	"testing"
)

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

// TestCompletionSeqSurvivesAClientSync checks a client push, which never
// carries the count, does not wipe it.
func TestCompletionSeqSurvivesAClientSync(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	report(t, sess, id, AgentStateWorking)
	report(t, sess, id, AgentStateDone)

	push := sess.GetState()
	for i := range push.Windows {
		push.Windows[i].CompletionSeq = 0
	}
	sess.UpdateState(push)
	if got := completionSeqOf(t, sess, id); got != 1 {
		t.Fatalf("completion_seq after a client sync = %d, want 1", got)
	}
	if got := sess.Info().Windows[0].CompletionSeq; got != 1 {
		t.Fatalf("listing completion_seq = %d, want 1", got)
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
