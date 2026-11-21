package session

import "time"

const agentHoldWindow = 700 * time.Millisecond

type agentHold struct {
	state  AgentState
	source AgentSource
	since  time.Time
}

func agentLoudness(state AgentState) int {
	switch state {
	case AgentStateNeedsInput, AgentStateErrored:
		return 3
	case AgentStateWorking, AgentStateLimitReached:
		return 2
	case AgentStateIdle, AgentStateDone, AgentStateUnknown:
		return 1
	default:
		return 0
	}
}

func (s *Session) holdQuieterState(windowID string, next, current AgentState, source AgentSource, now time.Time) bool {
	s.agentHoldMu.Lock()
	defer s.agentHoldMu.Unlock()
	if next == current {
		delete(s.agentHolds, windowID)
		return false
	}
	if agentLoudness(next) >= agentLoudness(current) {
		delete(s.agentHolds, windowID)
		return true
	}
	held, ok := s.agentHolds[windowID]
	if !ok || held.state != next {
		if s.agentHolds == nil {
			s.agentHolds = make(map[string]agentHold)
		}
		s.agentHolds[windowID] = agentHold{state: next, source: source, since: now}
		s.scheduleHoldSettleLocked(now)
		return false
	}
	if now.Sub(held.since) < agentHoldWindow {
		return false
	}
	delete(s.agentHolds, windowID)
	return true
}

func (s *Session) scheduleHoldSettleLocked(now time.Time) {
	if s.agentHoldTimer != nil || len(s.agentHolds) == 0 {
		return
	}
	earliest := now
	for _, h := range s.agentHolds {
		if h.since.Before(earliest) {
			earliest = h.since
		}
	}
	s.agentHoldTimer = time.AfterFunc(max(agentHoldWindow-now.Sub(earliest), time.Millisecond),
		s.settleHeldStates)
}

func (s *Session) settleHeldStates() {
	s.agentHoldMu.Lock()
	s.agentHoldTimer = nil
	s.agentHoldMu.Unlock()

	now := time.Now()
	s.settleAgentHolds(now)

	s.agentHoldMu.Lock()
	s.scheduleHoldSettleLocked(now)
	s.agentHoldMu.Unlock()
}

func (s *Session) settleAgentHolds(now time.Time) int {
	s.agentHoldMu.Lock()
	var due []struct {
		window string
		hold   agentHold
	}
	for id, held := range s.agentHolds {
		if now.Sub(held.since) >= agentHoldWindow {
			due = append(due, struct {
				window string
				hold   agentHold
			}{id, held})
			delete(s.agentHolds, id)
		}
	}
	s.agentHoldMu.Unlock()

	settled := 0
	for _, d := range due {
		if _, exists, agentPane := s.progressTarget(d.window); !exists || !agentPane {
			continue
		}
		if _, applied, err := s.ApplyAgentReport(d.window, AgentReport{State: d.hold.state, Source: d.hold.source}); err == nil && applied {
			settled++
		}
	}
	return settled
}

func (s *Session) stopAgentHoldTimer() {
	s.agentHoldMu.Lock()
	defer s.agentHoldMu.Unlock()
	if s.agentHoldTimer != nil {
		s.agentHoldTimer.Stop()
		s.agentHoldTimer = nil
	}
	clear(s.agentHolds)
}

func (s *Session) dropAgentHold(windowID string) {
	s.agentHoldMu.Lock()
	defer s.agentHoldMu.Unlock()
	delete(s.agentHolds, windowID)
}

func (s *Session) eventClaimWrittenOver(windowID string, claim agentClaim, wrote int64) bool {
	if !claim.event {
		return false
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	for i := range s.state.Windows {
		if s.state.Windows[i].ID == windowID {
			return wrote > s.state.Windows[i].AgentStateAt
		}
	}
	return false
}

func (c agentClaim) hookEventPrompt() bool {
	if c.blocker {
		return c.prior.event && c.prior.source == AgentSourceReport
	}
	return c.event && c.source == AgentSourceReport
}

func (s *Session) windowAgentState(windowID string) (AgentState, bool) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	for i := range s.state.Windows {
		if s.state.Windows[i].ID == windowID {
			return s.state.Windows[i].AgentState, true
		}
	}
	return AgentStateNone, false
}
