package session

import "time"

// agentHoldWindow is how long a quieter state has to keep being true before it
// is published. It exists because a sampled source can disagree with itself
// between two samples, and a pane whose glyph changes twice for one event reads
// as noise however correct each individual sample was.
//
// The value is short enough that a genuine change still feels immediate and long
// enough to swallow the gap between a harness clearing its progress bar and
// setting it again for the next step of the same task.
const agentHoldWindow = 700 * time.Millisecond

// agentHold is a quieter state waiting out agentHoldWindow.
type agentHold struct {
	state  AgentState
	source AgentSource
	since  time.Time
}

// agentLoudness ranks a state by how much it wants a human. The ordering is the
// whole anti-flicker policy: a transition that does not lower it is published at
// once, and only a transition that lowers it waits.
//
// It is deliberately asymmetric. Being slow to say "the agent needs you" costs
// the user the thing the feature exists to prevent, while being slow to say "the
// agent went quiet" costs them nothing.
func agentLoudness(state AgentState) int {
	switch state {
	case AgentStateNeedsInput, AgentStateErrored:
		return 3
	case AgentStateWorking:
		return 2
	case AgentStateIdle, AgentStateDone:
		return 1
	default: // AgentStateNone
		return 0
	}
}

// holdQuieterState decides whether a sampled source may publish next now.
//
// A state at or above the current loudness goes straight through, as does any
// state once it has stood unchanged for agentHoldWindow. A quieter state that has
// only just appeared is recorded and refused, so a source that flips and flips
// back inside the window produces one transition rather than two: the reversal
// finds the hold and cancels it, and nothing was ever published.
//
// A hold left behind by a source that then goes silent is settled by
// settleAgentHolds, so a state cannot be held indefinitely.
func (s *Session) holdQuieterState(windowID string, next, current AgentState, source AgentSource, now time.Time) bool {
	s.agentHoldMu.Lock()
	defer s.agentHoldMu.Unlock()
	if next == current {
		// Already there. Drop any hold, since what it was waiting to say is now
		// what the window already says.
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

// scheduleHoldSettleLocked arms the one-shot that publishes the earliest hold
// once its window elapses. Called with agentHoldMu held.
//
// The hold has to carry its own backstop. It used to rely on the stall monitor
// calling settleAgentHolds on its tick, which meant turning the silence timer
// off also stopped anything from ever publishing a held state: a harness that
// cleared its progress bar once and then went quiet stayed working forever. A
// timer belonging to the hold cannot be switched off by an unrelated setting.
//
// A pending timer is left alone rather than re-armed. Holds are recorded with
// since = now, so the earliest deadline never moves closer, and a timer that
// fires with nothing due simply re-arms for what is left.
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

// settleHeldStates is what the hold timer runs: publish whatever is due and arm
// again for anything still waiting. It is the only path that fires the timer, so
// clearing the field first is what lets the next hold arm a fresh one.
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

// settleAgentHolds publishes every held state whose window has elapsed and
// reports how many it published. It is the backstop for a source that says
// something quieter once and then goes silent: without it that state would wait
// for an event that never comes. Its caller is the hold's own timer, so no
// unrelated setting can switch the backstop off.
//
// The reports are applied after the lock is dropped, since ApplyAgentReport takes
// stateMu and holding two locks across it buys nothing.
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
		if _, applied, err := s.ApplyAgentReport(d.window, AgentReport{State: d.hold.state, Source: d.hold.source}); err == nil && applied {
			settled++
		}
	}
	return settled
}

// stopAgentHoldTimer disarms the backstop and forgets what it was waiting to
// say, for a session being stopped.
func (s *Session) stopAgentHoldTimer() {
	s.agentHoldMu.Lock()
	defer s.agentHoldMu.Unlock()
	if s.agentHoldTimer != nil {
		s.agentHoldTimer.Stop()
		s.agentHoldTimer = nil
	}
	clear(s.agentHolds)
}

// dropAgentHold forgets any hold on a window, so a window that goes away or whose
// state a stronger source took over does not leave one behind to be published
// later.
func (s *Session) dropAgentHold(windowID string) {
	s.agentHoldMu.Lock()
	defer s.agentHoldMu.Unlock()
	delete(s.agentHolds, windowID)
}

// windowAgentState returns the current agent state of a window and whether the
// window exists.
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
