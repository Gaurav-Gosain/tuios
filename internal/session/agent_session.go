package session

import "errors"

// AgentSessionReport is one set-agent-session call: a harness naming the
// conversation it runs in a pane, without saying anything about its state.
//
// Most harnesses with a hook surface cannot be trusted with the pane's state.
// Their hooks miss an interrupt, a cancelled approval or the end of a turn, and
// a state reported by a hook outranks every screen rule, so one missed event
// would pin the pane on working until the harness exits. What their hooks can
// say reliably is which conversation is running, which is what a resume needs.
// So those integrations report identity only, and the pane's state keeps coming
// from its screen rules, its title and the silence timer.
type AgentSessionReport struct {
	// Harness is the id of the harness reporting, as the manifests name it.
	Harness string
	// SessionID is the harness's own id for the conversation.
	SessionID string
	// HarnessPID is the pid of the harness process that ran the hook, 0 when
	// unknown. See applyAgentSession for what it decides.
	HarnessPID int
}

// errAgentSessionUnchanged tells mutateState that the report named the id the
// window already holds, so nothing is written or pushed.
var errAgentSessionUnchanged = errors.New("agent session is unchanged")

// applyAgentSession stores a reported conversation id on a window. It returns
// the id the window holds afterwards, whether the report was taken, and, when
// it was not, the reason as a wire value.
//
// It never changes the window's agent state, its source claim or its harness
// attribution: attribution is the foreground-process detector's to give, and a
// window this report attributed would read as an agent pane with no state.
//
// Two guards keep a nested run off the pane, the same two set-agent-state has:
//
//   - A window attributed to another harness refuses with foreign_harness. That
//     is a harness a tool call started inside the pane's own agent, and its
//     conversation is not the one a resume of the pane should reopen.
//   - A window that is working or needs_input, holds a different id, and got
//     that id from a known harness process refuses a report from a different
//     known process with foreign_session: a nested run of the same harness.
//     The same process may replace its own id (/clear, /resume), and a report
//     with no pid, or onto a window at rest, is taken, since at rest a new
//     conversation is exactly what it looks like.
//
// When the detector sees the agent leave the pane it forgets the pid, so a
// harness restarted in the same pane is not refused as a nested run of the one
// that exited.
func (s *Session) applyAgentSession(target string, r AgentSessionReport) (string, bool, string, error) {
	var stored string
	var reason string
	err := s.mutateState(func(st *SessionState) error {
		idx, err := findWindowStateIndex(st.Windows, target)
		if err != nil {
			return err
		}
		w := &st.Windows[idx]
		stored = w.AgentSessionID
		if w.AgentHarness != "" && w.AgentHarness != r.Harness {
			reason = agentRefusedForeignHarness
			return errAgentReportRefused(reason)
		}
		owner := s.agentHarnessPIDs[w.ID]
		if w.AgentSessionID == r.SessionID {
			if r.HarnessPID > 1 && owner != r.HarnessPID {
				s.setAgentHarnessPID(w.ID, r.HarnessPID)
			}
			return errAgentSessionUnchanged
		}
		busy := w.AgentState == AgentStateWorking || w.AgentState == AgentStateNeedsInput
		if busy && w.AgentSessionID != "" && owner > 1 && r.HarnessPID > 1 && owner != r.HarnessPID {
			reason = agentRefusedForeignSession
			return errAgentReportRefused(reason)
		}
		w.AgentSessionID = r.SessionID
		stored = r.SessionID
		s.setAgentHarnessPID(w.ID, r.HarnessPID)
		return nil
	})
	var refused errAgentReportRefused
	switch {
	case errors.Is(err, errAgentSessionUnchanged):
		return stored, true, "", nil
	case errors.As(err, &refused):
		return stored, false, reason, nil
	case err != nil:
		return "", false, "", err
	}
	return stored, true, "", nil
}

// setAgentHarnessPID records the harness pid a window's session id came from,
// forgetting it when the pid is unknown. Called with stateMu held.
func (s *Session) setAgentHarnessPID(windowID string, pid int) {
	if pid <= 1 {
		delete(s.agentHarnessPIDs, windowID)
		return
	}
	if s.agentHarnessPIDs == nil {
		s.agentHarnessPIDs = make(map[string]int)
	}
	s.agentHarnessPIDs[windowID] = pid
}
