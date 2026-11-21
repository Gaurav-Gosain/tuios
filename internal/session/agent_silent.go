package session

import (
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// AgentReasonSilent is WindowState.AgentReason for a hooked pane that
// reported working and then sent no report for the silent window.
const AgentReasonSilent = "silent"

// ShownAgentState is the state a reader is shown: unknown for a silent pane,
// and AgentState for every other.
func (w *WindowState) ShownAgentState() AgentState {
	if w.AgentReason == AgentReasonSilent && w.AgentState == AgentStateWorking {
		return AgentStateUnknown
	}
	return w.AgentState
}

func (s *Session) silentCandidate(w *WindowState) bool {
	if w.AgentState != AgentStateWorking || w.AgentReason != "" || w.AgentStateAt == 0 {
		return false
	}
	claim, held := s.agentClaims[w.ID]
	return held && claim.source.Rank() == AgentSourceReport.Rank() && !claim.blocker
}

func (s *Session) lastHeardLocked(w *WindowState) int64 {
	return max(w.AgentStateAt, s.agentClaims[w.ID].heardAt)
}

func (s *Session) noteHeardLocked(w *WindowState, claim agentClaim, held bool, r AgentReport) bool {
	if r.Source != AgentSourceReport || !held || claim.source.Rank() != AgentSourceReport.Rank() ||
		sessionGuard(w, claim, held, s.agentHarnessPIDs[w.ID].pid, s.agentSharedGuards[w.ID], r) != "" {
		return false
	}
	claim.heardAt = time.Now().UnixNano()
	s.setAgentClaim(w.ID, claim)
	if w.AgentReason == "" {
		return false
	}
	w.AgentReason = ""
	return true
}

func (s *Session) nextSilentAt(after time.Duration) (int64, bool) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	var next int64
	found := false
	for i := range s.state.Windows {
		w := &s.state.Windows[i]
		if !s.silentCandidate(w) {
			continue
		}
		if at := s.lastHeardLocked(w) + int64(after); !found || at < next {
			next, found = at, true
		}
	}
	return next, found
}

func (s *Session) markSilent(now time.Time, after time.Duration) int {
	if after <= 0 {
		return 0
	}
	cutoff := now.Add(-after).UnixNano()
	marked := 0
	_ = s.mutateState(func(st *SessionState) error {
		for i := range st.Windows {
			w := &st.Windows[i]
			if s.silentCandidate(w) && s.lastHeardLocked(w) <= cutoff {
				w.AgentReason = AgentReasonSilent
				marked++
			}
		}
		if marked == 0 {
			return errNoStallChange
		}
		return nil
	})
	return marked
}

func clearStaleSilenceLocked(before lifecycleSnapshot, st *SessionState) {
	for i := range st.Windows {
		w := &st.Windows[i]
		if w.AgentReason == "" {
			continue
		}
		idx, ok := before.index[w.ID]
		if w.AgentState != AgentStateWorking || !ok ||
			before.windows[idx].agentStateAt != w.AgentStateAt {
			w.AgentReason = ""
		}
	}
}

func resolveSilentAfter(cfg time.Duration) time.Duration {
	switch {
	case cfg < 0:
		return 0
	case cfg == 0:
		return config.DefaultSilentAfter
	}
	return cfg
}

type silentAfter struct{ ns atomic.Int64 }

// SetAgentSilentAfter applies [agents] silent_after. Zero turns it off. A
// change wakes silentMonitor, which works out its deadline again.
func (d *Daemon) SetAgentSilentAfter(after time.Duration) {
	after = max(after, 0)
	if d.silent.ns.Swap(int64(after)) != int64(after) {
		d.wakes.silent.Set()
	}
}

func (d *Daemon) silentMonitor() {
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for {
		if at, ok := d.nextSilentCheck(); ok {
			timer.Reset(time.Until(at))
		} else {
			timer.Stop()
		}
		select {
		case <-d.ctx.Done():
			return
		case <-d.wakes.silent.C():
			d.wakes.silent.Take()
		case <-timer.C:
			d.wakes.silentTicks.Add(1)
			after := time.Duration(d.silent.ns.Load())
			if d.agentsOff.Load() || after <= 0 {
				continue
			}
			now := time.Now()
			for _, sess := range d.manager.AllSessions() {
				sess.markSilent(now, after)
			}
		}
	}
}

func (d *Daemon) nextSilentCheck() (time.Time, bool) {
	after := time.Duration(d.silent.ns.Load())
	if after <= 0 || d.agentsOff.Load() {
		return time.Time{}, false
	}
	var next int64
	found := false
	for _, sess := range d.manager.AllSessions() {
		if at, ok := sess.nextSilentAt(after); ok && (!found || at < next) {
			next, found = at, true
		}
	}
	if !found {
		return time.Time{}, false
	}
	at := time.Unix(0, next)
	if now := time.Now(); !at.After(now) {
		at = now.Add(time.Second)
	}
	return at, true
}

func silentAfterFromConfig(a config.AgentsConfig) time.Duration {
	if d := a.SilentAfterDuration(); d > 0 {
		return d
	}
	return -1
}
