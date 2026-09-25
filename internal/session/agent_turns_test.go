package session

import (
	"testing"
)

func report(t *testing.T, sess *Session, windowID string, state AgentState) {
	t.Helper()
	if _, _, err := sess.ApplyAgentReport(windowID, AgentReport{State: state}); err != nil {
		t.Fatalf("ApplyAgentReport(%s): %v", state, err)
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
