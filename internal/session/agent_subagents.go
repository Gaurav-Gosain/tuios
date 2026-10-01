package session

import (
	"maps"
	"slices"
	"strconv"
	"time"
)

// The subagents an agent pane is running. A harness that hands work to
// subagents, or to the teammates of an agent team, and then ends its turn
// reports done, and its pane comes to rest, while that work goes on. So the
// daemon keeps, per pane, the subagents the pane's own hooks reported starting
// and not yet stopping. The window carries the count (AgentSubagents), which
// the rail draws, and the reserved metadata key subagents says it in words for
// get-agent-state and list-agents.
//
// It is display only, like the rest of the pane's metadata: nothing reads it
// to decide a state, a wait or an alert, and a subagent never changes the
// pane's state, its message or now. The reports come with
// report-agent-activity, which passes the identity guard first, so a nested
// run's subagents are not the pane's.
//
// The set is forgotten when the agent starts a conversation (the session_start
// activity of a new, resumed or cleared session), when the pane's state goes
// to none (the agent ended its session or left the pane), and when the window
// closes. A subagent the pane hears nothing more of for subagentQuiet is
// dropped as well: a stop that never came, after an interrupt, would
// otherwise leave a count on the row for as long as the agent runs. It is
// daemon memory only, so a daemon restart, which ends every process in every
// pane, starts it empty.

// subagentsMax bounds the subagents one pane keeps. A harness runs a handful
// at once, and an agent team's lead a few teammates; the cap only stops a
// caller growing a map without end. A start past it is not kept, so the count
// stays at the cap until one stops.
const subagentsMax = 64

// subagentQuiet is how long a subagent stays counted after the last event the
// pane reported for it, its start or a start repeated. Claude Code reports a
// subagent at its start and its stop and at nothing in between, so this is
// also the longest one can run and stay counted: an hour is past what a
// subagent or a teammate's stretch of work takes, and short enough that a
// missed stop does not outlive the afternoon.
const subagentQuiet = time.Hour

// subagent is one subagent a pane is running.
type subagent struct {
	agentType string
	// seen is when the pane last reported it, in unix nanoseconds.
	seen int64
}

// subagentEvent reports whether an activity event is a subagent's.
func subagentEvent(event string) bool {
	return event == ActivitySubagentStart || event == ActivitySubagentStop
}

// subagentChange is what one activity does to a pane's subagents: start or
// stop the one named, or forget them all. The zero value does nothing.
type subagentChange struct {
	// op is ActivitySubagentStart, ActivitySubagentStop or
	// ActivitySessionStart, or empty for nothing.
	op        string
	id        string
	agentType string
}

// subagentChangeOf is the change an activity report makes: a subagent's start
// or stop, or a new conversation, which starts with none.
func subagentChangeOf(r *AgentActivityReport) subagentChange {
	switch r.Event {
	case ActivitySubagentStart, ActivitySubagentStop:
		return subagentChange{op: r.Event, id: r.AgentID, agentType: attentionText(firstLine(r.AgentType), activityToolMax)}
	case ActivitySessionStart:
		return subagentChange{op: r.Event}
	}
	return subagentChange{}
}

// moveSubagentsLocked applies c, reported at now, to window w's subagents and
// returns how many it has after, and whether that count moved. A start of one
// already running counts as nothing but renews when it was last seen. A
// start past subagentsMax, a stop of one it does not know and a start on a
// pane with no agent state change nothing. The caller holds stateMu.
func (s *Session) moveSubagentsLocked(w *WindowState, c subagentChange, now int64) (int, bool) {
	set := s.agentSubagents[w.ID]
	switch c.op {
	case ActivitySubagentStart:
		if sa, ok := set[c.id]; ok {
			sa.seen = now
			set[c.id] = sa
			return len(set), false
		}
		if len(set) >= subagentsMax || w.AgentState == AgentStateNone {
			return len(set), false
		}
		if set == nil {
			set = make(map[string]subagent)
			if s.agentSubagents == nil {
				s.agentSubagents = make(map[string]map[string]subagent)
			}
			s.agentSubagents[w.ID] = set
		}
		set[c.id] = subagent{agentType: c.agentType, seen: now}
		return len(set), true
	case ActivitySubagentStop:
		if _, ok := set[c.id]; !ok {
			return len(set), false
		}
		delete(set, c.id)
		if len(set) == 0 {
			delete(s.agentSubagents, w.ID)
		}
		return len(set), true
	case ActivitySessionStart:
		// The window's count is checked as well as the set, so a count the
		// set lost some other way is not left on the row.
		if len(set) == 0 && w.AgentSubagents == 0 {
			return 0, false
		}
		delete(s.agentSubagents, w.ID)
		return 0, true
	}
	return len(set), false
}

// SubagentsText is how n subagents read on the rail and in the subagents
// key: "1 subagent" or "n subagents", and "" at zero.
func SubagentsText(n int) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return "1 subagent"
	}
	return strconv.Itoa(n) + " subagents"
}

// subagentsMetaValue is the subagents key for n subagents, and nil to remove
// the key at zero.
func subagentsMetaValue(n int) *string {
	if n <= 0 {
		return nil
	}
	v := SubagentsText(n)
	return &v
}

// withSubagentsKey is tokens with the subagents key saying n, or without it at
// zero. A pane that already holds as many keys as it may keeps its tokens as
// they are: the count the window carries still moves, and the rail draws that.
func withSubagentsKey(tokens []AgentMetaToken, n int, now int64) []AgentMetaToken {
	next, _, err := applyAgentMeta(tokens, AgentMetaUpdate{
		Keys: []string{AgentMetaSubagents}, Values: []*string{subagentsMetaValue(n)}, Source: agentMetaActivitySource,
	}, now)
	if err != nil {
		return tokens
	}
	return next
}

// forgetSubagentsLocked drops the subagents of every window that is gone or
// whose agent state is none, with the count and the key that say them. It
// runs inside every daemon-side mutation and after every client push, so the
// set goes however the agent left: a SessionEnd reported as none, the
// detector seeing the agent leave, set-agent-state none, or the window
// closing. The caller holds stateMu.
func (s *Session) forgetSubagentsLocked(st *SessionState) {
	if len(s.agentSubagents) == 0 {
		return
	}
	live := make(map[string]*WindowState, len(s.agentSubagents))
	for i := range st.Windows {
		if _, ok := s.agentSubagents[st.Windows[i].ID]; ok {
			live[st.Windows[i].ID] = &st.Windows[i]
		}
	}
	maps.DeleteFunc(s.agentSubagents, func(id string, _ map[string]subagent) bool {
		w, ok := live[id]
		if ok && w.AgentState != AgentStateNone {
			return false
		}
		if ok {
			w.AgentSubagents = 0
			if agentMetaValue(w.AgentMeta, AgentMetaSubagents) != "" {
				// A new slice, since a published snapshot may share the old one.
				w.AgentMeta = slices.DeleteFunc(slices.Clone(w.AgentMeta), func(t AgentMetaToken) bool { return t.Key == AgentMetaSubagents })
				if len(w.AgentMeta) == 0 {
					w.AgentMeta = nil
				}
			}
		}
		return true
	})
}

// subagentExpiryLocked is when the soonest of the session's subagents goes
// quiet, in unix nanoseconds, or 0 when it has none. The caller holds stateMu.
func (s *Session) subagentExpiryLocked() int64 {
	var at int64
	for _, set := range s.agentSubagents {
		for _, sa := range set {
			if due := sa.seen + int64(subagentQuiet); at == 0 || due < at {
				at = due
			}
		}
	}
	return at
}

// expireSubagents drops every subagent the session has heard nothing of for
// subagentQuiet as of now, moves the count and the key of each window that
// lost one, and arms the next prune. It reports how many it dropped. now is
// passed in so a test can stand at any time without waiting.
func (s *Session) expireSubagents(now time.Time) int {
	dropped := 0
	var next int64
	_ = s.mutateState(func(st *SessionState) error {
		cutoff := now.Add(-subagentQuiet).UnixNano()
		for i := range st.Windows {
			w := &st.Windows[i]
			set := s.agentSubagents[w.ID]
			gone := 0
			for id, sa := range set {
				if sa.seen <= cutoff {
					delete(set, id)
					gone++
				}
			}
			if gone == 0 {
				continue
			}
			dropped += gone
			if len(set) == 0 {
				delete(s.agentSubagents, w.ID)
			}
			w.AgentSubagents = len(set)
			w.AgentMeta = withSubagentsKey(w.AgentMeta, len(set), now.UnixNano())
		}
		next = s.subagentExpiryLocked()
		if dropped == 0 {
			// Nothing a client draws moved, so the version stays and nothing
			// is pushed.
			return errNoAgentMetaChange
		}
		return nil
	})
	s.armSubagentPrune(next)
	return dropped
}

// armSubagentPrune makes sure a prune runs by at, the way armAgentMetaPrune
// does for metadata with a TTL. A timer already due sooner stands. It is armed
// only while a pane has subagents, so a session without them costs nothing.
func (s *Session) armSubagentPrune(at int64) {
	if at == 0 {
		return
	}
	s.subagentMu.Lock()
	defer s.subagentMu.Unlock()
	if s.subagentTimer != nil && s.subagentAt != 0 && s.subagentAt <= at {
		return
	}
	if s.subagentTimer != nil {
		s.subagentTimer.Stop()
	}
	s.subagentAt = at
	s.subagentTimer = time.AfterFunc(max(time.Until(time.Unix(0, at)), 0), s.pruneSubagents)
}

// pruneSubagents is the timer's run: drop what went quiet, and arm the next.
func (s *Session) pruneSubagents() {
	s.subagentMu.Lock()
	s.subagentTimer, s.subagentAt = nil, 0
	s.subagentMu.Unlock()
	s.expireSubagents(time.Now())
}

// stopSubagentTimer cancels a pending prune, for a session that is stopping.
func (s *Session) stopSubagentTimer() {
	s.subagentMu.Lock()
	defer s.subagentMu.Unlock()
	if s.subagentTimer != nil {
		s.subagentTimer.Stop()
		s.subagentTimer, s.subagentAt = nil, 0
	}
}

// subagentCount is how many subagents the window's agent is running, as its
// hooks reported them.
func (s *Session) subagentCount(windowID string) int {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return len(s.agentSubagents[windowID])
}
