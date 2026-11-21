package session

import "time"

const agentMinTurn = 5 * time.Second

type agentTurn struct {
	since   int64
	workEnd int64
}

func agentStateFinishes(state AgentState) bool {
	return state == AgentStateIdle || state == AgentStateDone || state == AgentStateUnknown
}

func (s *Session) noteAgentTurnsLocked(before lifecycleSnapshot, now int64) {
	for i := range s.state.Windows {
		w := &s.state.Windows[i]
		idx, ok := before.index[w.ID]
		if !ok {
			continue
		}
		prev := before.windows[idx]
		if w.AgentState == prev.agentState {
			continue
		}
		turn, running := s.agentTurns[w.ID]
		switch {
		case w.AgentState == AgentStateWorking:
			if prev.agentState != AgentStateNeedsInput || !running {
				if s.agentTurns == nil {
					s.agentTurns = make(map[string]agentTurn)
				}
				s.agentTurns[w.ID] = agentTurn{since: now}
			}
		case prev.agentState == AgentStateWorking && agentStateFinishes(w.AgentState):
			since := turn.since
			if !running || since == 0 {
				since = prev.agentStateAt
			}
			end := now
			if turn.workEnd > 0 {
				end = turn.workEnd
			}
			if w.AgentState == AgentStateDone || end-since >= int64(agentMinTurn) {
				w.CompletionSeq++
			}
			delete(s.agentTurns, w.ID)
		case w.AgentState == AgentStateDone && s.agentClaims[w.ID].source == AgentSourceProgram:
			w.CompletionSeq++
			delete(s.agentTurns, w.ID)
		case w.AgentState != AgentStateNeedsInput:
			delete(s.agentTurns, w.ID)
		}
	}
	if len(s.agentTurns) > len(s.state.Windows) {
		live := make(map[string]struct{}, len(s.state.Windows))
		for i := range s.state.Windows {
			live[s.state.Windows[i].ID] = struct{}{}
		}
		for id := range s.agentTurns {
			if _, ok := live[id]; !ok {
				delete(s.agentTurns, id)
			}
		}
	}
}

func (s *Session) noteWorkEndLocked(windowID string, at int64) {
	turn, ok := s.agentTurns[windowID]
	if !ok {
		return
	}
	turn.workEnd = at
	s.agentTurns[windowID] = turn
}

func (s *Session) markCompletionSeenLocked(windowID string) {
	if windowID == "" {
		return
	}
	for i := range s.state.Windows {
		w := &s.state.Windows[i]
		if w.ID != windowID {
			continue
		}
		if s.completionSeen[w.ID] >= w.CompletionSeq {
			return
		}
		if s.completionSeen == nil {
			s.completionSeen = make(map[string]uint64)
		}
		s.completionSeen[w.ID] = w.CompletionSeq
		s.emit(SessionEvent{Type: eventCompletionSeen, Window: w.ID, completionSeq: w.CompletionSeq})
		return
	}
}

// MarkCompletionSeen records that the person has seen every turn a window has
// finished so far, which is what dismissing a finished item in the Inbox says.
func (s *Session) MarkCompletionSeen(windowID string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.markCompletionSeenLocked(windowID)
}

// MarkCompletionUnseen forgets that the person saw a window's last finished
// turn, which is what marking it unread in the Inbox says: the next client
// push with the window focused marks it seen again. It reports whether the
// window has finished a turn at all.
func (s *Session) MarkCompletionUnseen(windowID string) bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	for i := range s.state.Windows {
		w := &s.state.Windows[i]
		if w.ID != windowID {
			continue
		}
		if w.CompletionSeq == 0 {
			return false
		}
		if s.completionSeen[w.ID] >= w.CompletionSeq {
			s.completionSeen[w.ID] = w.CompletionSeq - 1
			s.emit(SessionEvent{Type: eventCompletionUnseen, Window: w.ID, completionSeq: w.CompletionSeq})
		}
		return true
	}
	return false
}

func (s *Session) finishedUnread(w *WindowState) bool {
	if w.CompletionSeq == 0 || !agentStateFinishes(w.AgentState) {
		return false
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return w.CompletionSeq > s.completionSeen[w.ID]
}

func (s *Session) withWindowState(windowID string, fn func(w WindowState, found bool)) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	for i := range s.state.Windows {
		if s.state.Windows[i].ID == windowID {
			fn(s.state.Windows[i], true)
			return
		}
	}
	fn(WindowState{}, false)
}
