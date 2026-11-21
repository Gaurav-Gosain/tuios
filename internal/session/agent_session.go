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
	HarnessPID  int
	foreignPane bool
	// HarnessIsCaller is AgentReport.HarnessIsCaller.
	HarnessIsCaller bool
	// TranscriptOnly is a metadata update for the conversation already in
	// the pane, never a request to replace it with a different conversation.
	TranscriptOnly bool
	harnessGone    bool
}

var errAgentSessionUnchanged = errors.New("agent session is unchanged")

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
		if r.foreignPane {
			reason = agentRefusedForeignPane
			return errAgentReportRefused(reason)
		}
		if w.AgentHarness != "" && w.AgentHarness != r.Harness {
			reason = agentRefusedForeignHarness
			return errAgentReportRefused(reason)
		}
		if r.TranscriptOnly && w.AgentSessionID != "" && w.AgentSessionID != r.SessionID {
			reason = agentRefusedForeignSession
			return errAgentReportRefused(reason)
		}
		owner := s.agentHarnessPIDs[w.ID].pid
		if w.AgentSessionID == r.SessionID && w.AgentSessionHarness == r.Harness {
			if r.HarnessPID > 1 && owner != r.HarnessPID {
				s.setAgentHarnessPID(w.ID, r.HarnessPID, r.HarnessIsCaller, r.harnessGone)
			}
			return errAgentSessionUnchanged
		}
		busy := w.AgentState == AgentStateWorking || w.AgentState == AgentStateNeedsInput
		if busy && w.AgentSessionID != "" && owner > 1 && r.HarnessPID > 1 && owner != r.HarnessPID {
			reason = agentRefusedForeignSession
			return errAgentReportRefused(reason)
		}
		w.AgentSessionID = r.SessionID
		w.AgentSessionHarness = r.Harness
		stored = r.SessionID
		s.setAgentHarnessPID(w.ID, r.HarnessPID, r.HarnessIsCaller, r.harnessGone)
		delete(s.agentSharedGuards, w.ID)
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

func (s *Session) setAgentHarnessPID(windowID string, pid int, isCaller, gone bool) {
	if pid <= 1 && !gone {
		delete(s.agentHarnessPIDs, windowID)
		return
	}
	if old, ok := s.agentHarnessPIDs[windowID]; ok && pid > 1 && old.pid == pid && old.timed {
		return
	}
	if s.agentHarnessPIDs == nil {
		s.agentHarnessPIDs = make(map[string]harnessProc)
	}
	h := harnessProc{pid: max(pid, 0)}
	switch {
	case gone:
		h.gone = true
	case isCaller:
		h = readHarnessProc(pid)
	}
	s.agentHarnessPIDs[windowID] = h
}

func callerRunsHarness(cs *connState, pid int) bool {
	if pid <= 1 {
		return false
	}
	if cs == nil {
		return true
	}
	cur := cs.peerPID
	for range paneOriginMaxDepth {
		if cur <= 1 {
			return false
		}
		if cur == pid {
			return true
		}
		next, _, ok := readProcLineage(cur)
		if !ok {
			return false
		}
		cur = next
	}
	return false
}
