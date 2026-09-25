package session

import (
	"testing"
)

// bareSessionWithWindow builds a session with one window but no daemon socket, so
// the stall heuristic can be exercised directly without any network.
func bareSessionWithWindow(t *testing.T) (*Session, string) {
	t.Helper()
	t.Cleanup(useResurrectionDir(t.TempDir()))
	sess, err := NewSession("stall", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	win, err := sess.AddDaemonWindow("Window", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	t.Cleanup(sess.Stop)
	return sess, win.ID
}

func agentStateOf(t *testing.T, sess *Session, windowID string) AgentState {
	t.Helper()
	for _, w := range sess.GetState().Windows {
		if w.ID == windowID {
			return w.AgentState
		}
	}
	t.Fatalf("window %s not found", windowID)
	return AgentStateNone
}

// TestAgentStateRetainedAcrossClientSync checks a client state sync that omits
// agent fields does not wipe them: the daemon carries them over by window id.
func TestAgentStateRetainedAcrossClientSync(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	if err := sess.SetDaemonWindowAgentState(id, AgentStateWorking, "building"); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}

	// A client sync built from the daemon state but with the agent fields cleared,
	// exactly as a client (which never sets them) would send.
	incoming := sess.GetState()
	incoming.BaseVersion = incoming.Version
	for i := range incoming.Windows {
		incoming.Windows[i].AgentState = AgentStateNone
		incoming.Windows[i].AgentMessage = ""
		incoming.Windows[i].AgentStateAt = 0
	}
	sess.UpdateState(incoming)

	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("agent state after client sync = %q, want working (should be retained)", got)
	}
	for _, w := range sess.GetState().Windows {
		if w.ID == id && w.AgentMessage != "building" {
			t.Fatalf("agent message after client sync = %q, want building", w.AgentMessage)
		}
	}
}
