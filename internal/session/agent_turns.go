package session

import "time"

// Finished turns, counted.
//
// done used to be the only state that meant "the agent finished", and only an
// explicit report or the Claude transcript produces it. A Codex or Gemini pane
// with no hook went working, then unknown on the silence timer, and never told
// anybody it was done. herdr's answer is that done is simply "back at rest and
// not yet looked at", and it works for every agent.
//
// So the daemon counts turns. Every time a window goes from working to a rest
// state (idle, done or unknown) after working for at least agentMinTurn, its
// CompletionSeq goes up by one. A client compares that number with the one it
// had when its user last focused the pane, which makes "finished and unread"
// a per-client fact, as it has to be: two people attached to one session have
// not both looked at a pane because one of them did.
//
// The minimum is what keeps a redraw from counting. A pane the detector owns
// goes to working on any output from the agent, a cursor blink included, and
// back to unknown when the silence timer runs; a real turn does not take less
// than a few seconds. An explicit done needs no minimum, since it is the agent
// saying so.
const agentMinTurn = 5 * time.Second

// agentTurn is one window's working phase in progress.
type agentTurn struct {
	// since is when the window went to working, in unix nanoseconds.
	since int64
	// workEnd is when the work last showed, set by the silence timer as it
	// demotes the pane: the turn ended when the pane went quiet, not when the
	// timer noticed thirty seconds later.
	workEnd int64
}

// agentStateFinishes reports whether moving from working to state finishes a
// turn. needs_input does not: the agent is waiting in the middle of it.
// errored does not either: it has its own alert, and a failure is not a
// finished piece of work to review.
func agentStateFinishes(state AgentState) bool {
	return state == AgentStateIdle || state == AgentStateDone || state == AgentStateUnknown
}

// noteAgentTurnsLocked counts finished turns between before and the current
// state, bumping CompletionSeq on each window that finished one. It runs inside
// every daemon-side mutation, after the mutation and before the snapshot, so a
// bump reaches clients with the state change that caused it. The caller holds
// stateMu.
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
			// Back to work after a question keeps the turn it interrupted.
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

// noteWorkEndLocked records when a window's work last showed, for the silence
// timer to call before it demotes the window. The caller holds stateMu.
func (s *Session) noteWorkEndLocked(windowID string, at int64) {
	turn, ok := s.agentTurns[windowID]
	if !ok {
		return
	}
	turn.workEnd = at
	s.agentTurns[windowID] = turn
}

// markCompletionSeenLocked records that an attached client has the window
// focused, which is what the daemon's view of "finished and unread" clears on.
// The caller holds stateMu.
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
		// The attention queue closes the pane's finished item on this. It is
		// emitted under the state lock like every other event, so it lands
		// after the transition that opened the item.
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

// finishedUnread reports the daemon's view of whether a window finished a turn
// nobody has looked at: its CompletionSeq is past the one it had when an
// attached client last pushed state with it focused, and it is still at rest.
// A client keeps its own record for its rail; this is the answer for a script,
// which has no focus of its own. It takes the state read lock.
func (s *Session) finishedUnread(w *WindowState) bool {
	if w.CompletionSeq == 0 || !agentStateFinishes(w.AgentState) {
		return false
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return w.CompletionSeq > s.completionSeen[w.ID]
}
