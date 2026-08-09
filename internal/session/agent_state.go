package session

import "time"

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

// SetDaemonWindowAgentState records the agent state (and an optional short
// message) on the window matching target, stamping the time it was set. It runs
// through mutateState, so it bumps the session version and reaches attached
// clients through the same state-push every other daemon-side mutation uses.
//
// This is an explicit report. The output-stall heuristic never overrides it: the
// heuristic only ever moves a window out of AgentStateWorking, and only after the
// pane has been silent for the configured window, so any state set here other
// than working is left untouched, and a fresh working report resets the silence
// clock.
func (s *Session) SetDaemonWindowAgentState(target string, state AgentState, message string) error {
	return s.mutateState(func(st *SessionState) error {
		idx, err := findWindowStateIndex(st.Windows, target)
		if err != nil {
			return err
		}
		st.Windows[idx].AgentState = state
		st.Windows[idx].AgentMessage = message
		st.Windows[idx].AgentStateAt = time.Now().UnixNano()
		return nil
	})
}

// applyStallHeuristic moves any window that has been silently working for at
// least stall into AgentStateIdle, and reports how many it moved. It is the
// fallback for agents that do not report their own state: a pane that reported
// working but has produced no output for the stall window has most likely gone
// idle or is waiting on the user, so it is demoted to idle rather than left
// looking busy forever.
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
// now and stall are passed in, and lastOutput returns the unix-nano time of a
// PTY's most recent output (0 when unknown), so the whole rule is deterministic
// and unit-testable without real timers. A stall <= 0 disables the heuristic.
func (s *Session) applyStallHeuristic(now time.Time, stall time.Duration, lastOutput func(ptyID string) int64) int {
	if stall <= 0 {
		return 0
	}
	cutoff := now.Add(-stall).UnixNano()
	flipped := 0
	_ = s.mutateState(func(st *SessionState) error {
		for i := range st.Windows {
			w := &st.Windows[i]
			if w.AgentState != AgentStateWorking {
				continue
			}
			lastActivity := w.AgentStateAt
			if w.PTYID != "" {
				if out := lastOutput(w.PTYID); out > lastActivity {
					lastActivity = out
				}
			}
			if lastActivity <= cutoff {
				w.AgentState = AgentStateIdle
				w.AgentStateAt = now.UnixNano()
				flipped++
			}
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

// errNoStallChange is a sentinel used by applyStallHeuristic to tell mutateState
// that a tick changed nothing, so it neither bumps the version nor pushes state.
// It never leaves the package.
var errNoStallChange = stallNoChange{}

type stallNoChange struct{}

func (stallNoChange) Error() string { return "no agent-state change" }
