package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// agentRestoreState is one session holding a single pane the daemon has already
// detected an agent in, which is what a client attaching or switching to a
// session with a running agent is handed.
func agentRestoreState() *session.SessionState {
	return &session.SessionState{
		Name:             "work",
		CurrentWorkspace: 1,
		Windows: []session.WindowState{{
			ID: "w1", PTYID: "p1", Title: "claude", Workspace: 1,
			X: 0, Y: 0, Width: 80, Height: 24,
			AgentState:    session.AgentStateWorking,
			AgentMessage:  "building",
			AgentStateAt:  1234,
			ForegroundCmd: "claude",
		}},
	}
}

// TestRestoreKeepsDaemonOwnedAgentFields is the reported bug: the pane you are
// looking at never appears in the rail's agents section. Agent state is
// daemon-owned and reaches the attached session only by being copied onto the
// live window; the restore path copied the layout fields and dropped the agent
// ones, so every pane of the session you are in came back with no agent state
// and the section that lists them was built from nothing.
//
// Foreign sessions were unaffected because the rail rebuilds those from the
// daemon's polled listing, which is why the section could show other people's
// agents and never your own.
func TestRestoreKeepsDaemonOwnedAgentFields(t *testing.T) {
	m := &OS{Settings: config.Global}
	if err := m.RestoreFromState(agentRestoreState()); err != nil {
		t.Fatalf("RestoreFromState: %v", err)
	}
	if len(m.Windows) != 1 {
		t.Fatalf("restored %d windows, want 1", len(m.Windows))
	}

	w := m.Windows[0]
	if w.AgentState != string(session.AgentStateWorking) {
		t.Errorf("AgentState = %q after a restore, want %q: the pane cannot reach the agents section",
			w.AgentState, session.AgentStateWorking)
	}
	if w.AgentMessage != "building" {
		t.Errorf("AgentMessage = %q, want %q", w.AgentMessage, "building")
	}
	if w.AgentStateAt != 1234 {
		t.Errorf("AgentStateAt = %d, want 1234: the row cannot say how long the state has held", w.AgentStateAt)
	}
	if w.ForegroundCmd != "claude" {
		t.Errorf("ForegroundCmd = %q, want %q: the row falls back to the shell title", w.ForegroundCmd, "claude")
	}
}
