package app

import (
	"fmt"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// AgentReport is what a pane says about the agent running in it: the fields
// the set-agent-state verb takes, minus the ones only a daemon can use.
type AgentReport struct {
	// WindowID is the pane the report is about.
	WindowID string
	// State is one of session.AgentStateNames. "none" clears it.
	State string
	// Message is the optional short note shown with the state.
	Message string
	// Kind says what a needs_input state waits for: "approval" or "question".
	Kind string
	// Harness is the harness id of the reporting agent, such as "claude-code".
	Harness string
}

// AgentReportMsg carries an AgentReport onto the Update goroutine, for a
// reporter that runs on another one.
type AgentReportMsg AgentReport

// ReportAgentState applies an agent report to a pane of this client, with no
// daemon in between. It is for a session that has no daemon to send
// set-agent-state to: the browser build, whose fake agent reports through it.
//
// The pane ends up exactly as a daemon session's pane does when the daemon
// syncs a report to it: the note, kind and harness are adopted first, and the
// state lands last through noteAgentState, so the rail, the title glyph, the
// unread mark and the alert policy all see the same transition.
func (m *OS) ReportAgentState(r AgentReport) error {
	state, ok := session.ParseAgentState(r.State)
	if !ok {
		return fmt.Errorf("unknown agent state %q", r.State)
	}
	if r.Kind != "" && state != session.AgentStateNeedsInput {
		return fmt.Errorf("kind describes a needs_input state, and this report is %s", r.State)
	}
	w := m.windowByID(r.WindowID)
	if w == nil {
		return fmt.Errorf("no window %q", r.WindowID)
	}
	to := string(state)
	if w.AgentState != to {
		w.AgentStateAt = time.Now().UnixNano()
		if state == session.AgentStateDone {
			w.AgentCompletionSeq++
		}
	}
	w.AgentMessage = r.Message
	w.AgentKind = r.Kind
	if r.Harness != "" || state == session.AgentStateNone {
		w.AgentHarness = r.Harness
	}
	m.noteAgentState(w, to)
	w.InvalidateCache()
	m.MarkAllDirty()
	return nil
}
