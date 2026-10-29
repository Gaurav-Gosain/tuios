package session

import (
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Silent agents (#898).
//
// A harness with hooks reports working at every prompt and every tool call,
// so its reports are a heartbeat. When the harness hangs (an API call that
// never returns, a deadlocked tool, a wedged process) the reports stop, and
// the pane said working for as long as it lived. The silence timer does not
// catch it: its clock is the later of the last report and the pane's last
// output, and a hung harness keeps its spinner going.
//
// So a pane whose claim is a report, and whose state is working, gets a second
// clock that only reports move. When it runs out the pane is shown as unknown
// with the reason silent. AgentState stays working underneath: every rule that
// decides what to type into a pane, when a turn ends, and when mail wakes an
// agent keeps reading the state the agent last gave, so a hung pane is never
// taken for one at rest. Only the readers that show a state to a person or a
// script read the shown state: get-agent-state, list-agents, the agent-state
// event, the session listing and the client.
//
// The next report clears the reason, whatever it says, because it sets the
// state again (clearStaleSilenceLocked). Panes the detector or a screen rule
// holds have no heartbeat to miss and are left to the silence timer, which
// already moves a working pane whose screen stops changing.

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

// silentCandidate reports whether a window can go silent: it is working, it
// is not silent already, and its claim is a report, which is what a hook or a
// harness reporting for itself sends.
func (s *Session) silentCandidate(w *WindowState) bool {
	if w.AgentState != AgentStateWorking || w.AgentReason != "" || w.AgentStateAt == 0 {
		return false
	}
	claim, held := s.agentClaims[w.ID]
	return held && claim.source.rank() == AgentSourceReport.rank() && !claim.blocker
}

// lastHeardLocked is when the agent of a window last reported: the later of
// the state it set and a report since that did not set one. The caller holds
// stateMu.
func (s *Session) lastHeardLocked(w *WindowState) int64 {
	return max(w.AgentStateAt, s.agentClaims[w.ID].heardAt)
}

// noteHeardLocked records a report the pane's own agent sent that did not set
// the state, and reports whether it cleared a silence, which is a change to
// publish. Only a report the identity guard lets through counts. The caller
// holds stateMu.
func (s *Session) noteHeardLocked(w *WindowState, claim agentClaim, held bool, r AgentReport) bool {
	if r.Source != AgentSourceReport || !held || claim.source.rank() != AgentSourceReport.rank() ||
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

// nextSilentAt is when the earliest candidate window of this session goes
// silent, in unix nanoseconds, and false when there is none.
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

// markSilent marks every candidate window whose last report is older than
// after, and reports how many it marked. AgentStateAt is left alone: it is
// still when the agent last reported, which is what the row's age and
// evidence_age_ms say.
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

// clearStaleSilenceLocked drops the silent reason from a window whose state
// was set again since before: a report came, or something else moved the
// pane. It runs inside every daemon-side mutation, before the events are
// diffed, so the event that a report raises already shows the reported state.
// The caller holds stateMu.
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

// resolveSilentAfter turns DaemonConfig.AgentSilentAfter into the window:
// zero is the default, and a negative value is off.
func resolveSilentAfter(cfg time.Duration) time.Duration {
	switch {
	case cfg < 0:
		return 0
	case cfg == 0:
		return config.DefaultSilentAfter
	}
	return cfg
}

// silentAfter holds the daemon's silent window, as nanoseconds. Zero turns
// the silent reason off.
type silentAfter struct{ ns atomic.Int64 }

// SetAgentSilentAfter applies [agents] silent_after. Zero turns it off. A
// change wakes silentMonitor, which works out its deadline again.
func (d *Daemon) SetAgentSilentAfter(after time.Duration) {
	after = max(after, 0)
	if d.silent.ns.Swap(int64(after)) != int64(after) {
		d.wakes.silent.Set()
	}
}

// silentMonitor marks hooked panes silent. It holds one timer, armed only
// while some pane is working on a report and not yet silent, for the earliest
// such pane; with none it sleeps on its latch, which a change to any
// session's state raises. A report restamps the pane's state, so it raises
// the latch and moves the deadline.
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

// nextSilentCheck is when silentMonitor next looks, and false when nothing
// can go silent.
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
	// A deadline already past that the last look did not mark is a clock
	// step, not a pane to mark: look again in a second rather than spin.
	at := time.Unix(0, next)
	if now := time.Now(); !at.After(now) {
		at = now.Add(time.Second)
	}
	return at, true
}

// silentAfterFromConfig is [agents] silent_after as DaemonConfig spells it:
// negative for off.
func silentAfterFromConfig(a config.AgentsConfig) time.Duration {
	if d := a.SilentAfterDuration(); d > 0 {
		return d
	}
	return -1
}
