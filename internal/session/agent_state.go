package session

import (
	"errors"
	"time"
)

// AgentState is the semantic state of an agent (a coding-agent CLI or any other
// long-running process) running in a window's pane. It is daemon-owned per-window
// state: a pane reports its own state through the set-agent-state verb, and the
// daemon syncs it to attached clients alongside the rest of the window state.
//
// The zero value is AgentStateNone, which is also what a pane not running an
// agent reports. Storing none as the empty string keeps it out of serialized
// state entirely (omitempty), so older on-disk state and older clients that never
// heard of agent state read back as none, which is exactly the pre-existing
// behavior.
type AgentState string

const (
	// AgentStateNone is the default: the pane is not running an agent, or is not
	// reporting. It serializes as the empty string so it is omitted from state.
	AgentStateNone AgentState = ""
	// AgentStateWorking means the agent is actively working on a task.
	AgentStateWorking AgentState = "working"
	// AgentStateNeedsInput means the agent is blocked waiting for the user.
	AgentStateNeedsInput AgentState = "needs_input"
	// AgentStateIdle means the agent is not working and not blocked; it is the
	// state the output-stall heuristic assigns to a pane that went quiet.
	AgentStateIdle AgentState = "idle"
	// AgentStateDone means the agent finished its task.
	AgentStateDone AgentState = "done"
	// AgentStateErrored means the agent stopped because of an error.
	AgentStateErrored AgentState = "errored"
)

// agentStateByName maps every accepted wire value to its AgentState. "none" is
// the explicit spelling a caller uses to clear the state; it maps to the empty
// AgentStateNone.
var agentStateByName = map[string]AgentState{
	"none":        AgentStateNone,
	"working":     AgentStateWorking,
	"needs_input": AgentStateNeedsInput,
	"idle":        AgentStateIdle,
	"done":        AgentStateDone,
	"errored":     AgentStateErrored,
}

// AgentStateNames lists the accepted wire values in a stable order, for the
// verb's accepted-value schema and for input validation. It is part of the
// public protocol surface; keep the values stable.
var AgentStateNames = []string{"none", "working", "needs_input", "idle", "done", "errored"}

// ParseAgentState resolves a wire value to an AgentState, reporting whether the
// value was one of the accepted names. An empty input is not accepted here: the
// verb requires the caller to name a state, and "none" is the spelling that
// clears it.
func ParseAgentState(s string) (AgentState, bool) {
	if s == "" {
		return AgentStateNone, false
	}
	v, ok := agentStateByName[s]
	return v, ok
}

// Name returns the wire spelling of the state, mapping the empty AgentStateNone
// back to "none" so a reader always gets an explicit value.
func (a AgentState) Name() string {
	if a == AgentStateNone {
		return "none"
	}
	return string(a)
}

// AgentReport is one source's claim on a window's agent state. Source empty
// means AgentSourceReport, so the zero value is an explicit report, which is
// what every caller predating sources sends.
type AgentReport struct {
	State   AgentState
	Message string
	Source  AgentSource
	Harness string // optional harness id, reported back by get-agent-state
}

// SetDaemonWindowAgentState records an explicit report on the window matching
// target. It is the no-source path: every caller that names no source is
// reporting for itself and gets AgentSourceReport, the highest rank, which is
// exactly the authority such a caller had before sources existed.
func (s *Session) SetDaemonWindowAgentState(target string, state AgentState, message string) error {
	_, _, err := s.ApplyAgentReport(target, AgentReport{State: state, Message: message})
	return err
}

// ApplyAgentReport records r on the window matching target, stamping the time it
// was set, and returns the window's effective state afterwards and whether r was
// the thing that set it. It runs through mutateState, so an applied report bumps
// the session version and reaches attached clients through the same state-push
// every other daemon-side mutation uses.
//
// A report from a source ranked below the one that currently owns the window is
// refused, and refusing is not an error: a screen rule guessing at a pane whose
// harness reports for itself is the ordinary case, and the weaker guess has to
// leave the better answer alone. A refused report changes nothing, so it neither
// bumps the version nor pushes.
//
// The output-stall heuristic is deliberately not routed through here; see
// applyStallHeuristic for why.
func (s *Session) ApplyAgentReport(target string, r AgentReport) (AgentState, bool, error) {
	if r.Source == "" {
		r.Source = AgentSourceReport
	}
	var effective AgentState
	applied := false
	err := s.mutateState(func(st *SessionState) error {
		idx, err := findWindowStateIndex(st.Windows, target)
		if err != nil {
			return err
		}
		w := &st.Windows[idx]
		claim, held := s.agentClaims[w.ID]
		// held, not the zero claim's rank: a window nobody has claimed is open to
		// any source, including the weakest.
		if held && r.Source.rank() < claim.source.rank() {
			effective = w.AgentState
			return errAgentClaimHeld
		}
		w.AgentState = r.State
		w.AgentMessage = r.Message
		w.AgentHarness = r.Harness
		w.AgentStateAt = time.Now().UnixNano()
		// auto is carried over: it says the detector will clear this pane when the
		// agent exits, which a report taking the state over does not change.
		s.setAgentClaim(w.ID, agentClaim{source: r.Source, harness: r.Harness, auto: claim.auto})
		effective = r.State
		applied = true
		return nil
	})
	if errors.Is(err, errAgentClaimHeld) {
		return effective, false, nil
	}
	if err != nil {
		return effective, false, err
	}
	return effective, applied, nil
}

// errAgentClaimHeld tells mutateState that a higher-ranked source owns the
// window, so the refused report neither bumps the version nor pushes state. It
// never leaves the package.
var errAgentClaimHeld = agentClaimHeld{}

type agentClaimHeld struct{}

func (agentClaimHeld) Error() string { return "agent state is held by a higher-ranked source" }

// applyStallHeuristic moves any window that has been silently working for at
// least stall into AgentStateIdle, and reports how many it moved. It is the
// fallback for agents that do not report their own state: a pane that reported
// working but has produced no output for the stall window has most likely gone
// idle, so it is demoted rather than left looking busy forever.
//
// Silence on its own is not evidence of finishing, and that is why look exists.
// A harness waiting on a human paints its question and then emits nothing at
// all, no title and no progress sequence, which is byte for byte the same
// silence as a harness that finished. Demoting on the timer alone therefore
// prints idle over a pane that is blocked, and idle reads as "fine and done" and
// raises no alert, so the user is told the opposite of the truth at exactly the
// moment the feature exists to serve.
//
// look is given each stalled pane's PTY and reports whether the screen tier
// found a rule matching it. A pane whose screen answers is left alone: the
// answer came from looking, and looking beats a timer. A nil look, or a harness
// with no screen rules, restores the timer-only behaviour, which is still the
// best available when there is nothing to read.
//
// It is deliberately conservative and strictly secondary to explicit reporting:
//
//   - It only ever reads AgentStateWorking and only ever writes AgentStateIdle.
//     Any window in any other state is untouched, so an explicit needs_input,
//     done, or errored report is never overridden.
//   - The silence clock is the later of the window's last output and the time
//     its working state was set, so an agent that is genuinely working (and thus
//     producing output) is never demoted, and a working report just made is given
//     the full stall window before it can be demoted.
//   - It never promotes a pane into working; only an explicit report does that.
//
// Those three rules are why it writes state directly instead of going through
// ApplyAgentReport's precedence gate: reading only working and writing only idle
// is already narrower than the gate, and gating it would silently stop it
// demoting a reported working state, which is the case it exists for. It does
// record itself as the window's source afterwards, so get-agent-state can say
// the idle came from the silence timer rather than from the agent.
//
// now and stall are passed in, and lastOutput returns the unix-nano time of a
// PTY's most recent output (0 when unknown), so the whole rule is deterministic
// and unit-testable without real timers. A stall <= 0 disables the heuristic.
func (s *Session) applyStallHeuristic(now time.Time, stall time.Duration, lastOutput func(ptyID string) int64, look func(ptyID string) bool) int {
	if stall <= 0 {
		return 0
	}
	cutoff := now.Add(-stall).UnixNano()

	// Candidates are collected before anything is written, because look reads a
	// pane's screen and may publish a state of its own, and that goes through
	// ApplyAgentReport, which takes the lock mutateState is holding.
	type candidate struct{ windowID, ptyID string }
	var pending []candidate
	s.stateMu.RLock()
	for i := range s.state.Windows {
		w := &s.state.Windows[i]
		if w.AgentState == AgentStateWorking && stalledAt(w.AgentStateAt, w.PTYID, cutoff, lastOutput) {
			pending = append(pending, candidate{w.ID, w.PTYID})
		}
	}
	s.stateMu.RUnlock()
	if len(pending) == 0 {
		return 0
	}

	if look != nil {
		kept := pending[:0]
		for _, c := range pending {
			if c.ptyID != "" && look(c.ptyID) {
				continue
			}
			kept = append(kept, c)
		}
		pending = kept
	}

	flipped := 0
	_ = s.mutateState(func(st *SessionState) error {
		for _, c := range pending {
			idx, err := findWindowStateIndex(st.Windows, c.windowID)
			if err != nil {
				continue
			}
			w := &st.Windows[idx]
			// Re-checked because look ran between the two passes and may have
			// moved the pane out of working, which is the whole point of it.
			if w.AgentState != AgentStateWorking || !stalledAt(w.AgentStateAt, w.PTYID, cutoff, lastOutput) {
				continue
			}
			w.AgentState = AgentStateIdle
			w.AgentStateAt = now.UnixNano()
			claim := s.agentClaims[w.ID]
			claim.source = AgentSourceStall
			s.setAgentClaim(w.ID, claim)
			flipped++
		}
		if flipped == 0 {
			// Returning an error makes mutateState skip the version bump and the
			// client push, so a quiet tick that changed nothing is free.
			return errNoStallChange
		}
		return nil
	})
	return flipped
}

// stalledAt reports whether a window has been silent since cutoff, taking the
// later of when its working state was set and when its pane last wrote.
func stalledAt(stateAt int64, ptyID string, cutoff int64, lastOutput func(ptyID string) int64) bool {
	if ptyID != "" {
		if out := lastOutput(ptyID); out > stateAt {
			stateAt = out
		}
	}
	return stateAt <= cutoff
}

// errNoStallChange is a sentinel used by applyStallHeuristic to tell mutateState
// that a tick changed nothing, so it neither bumps the version nor pushes state.
// It never leaves the package.
var errNoStallChange = stallNoChange{}

type stallNoChange struct{}

func (stallNoChange) Error() string { return "no agent-state change" }
