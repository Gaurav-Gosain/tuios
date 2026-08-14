package session

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// agentStateForProgress maps an OSC 9;4 progress report onto an agent state.
//
// The mapping follows the sequence's published meaning rather than any one
// harness's habits: a determinate or indeterminate bar is the program saying it
// is busy, clearing the bar is it saying it stopped, and the error state is it
// saying the operation failed. The warning state is the only one that carries a
// judgement, and a program that flags its own progress as needing attention is
// asking for a human, which is what needs_input means here.
//
// Clearing maps to idle rather than done because the sequence says the work
// stopped and says nothing about whether it succeeded. done is a claim only the
// harness itself can honestly make, through a report.
func agentStateForProgress(state vt.ProgressState) (AgentState, bool) {
	switch state {
	case vt.ProgressClear:
		return AgentStateIdle, true
	case vt.ProgressNormal, vt.ProgressIndeterminate:
		return AgentStateWorking, true
	case vt.ProgressError:
		return AgentStateErrored, true
	case vt.ProgressWarning:
		return AgentStateNeedsInput, true
	default:
		return AgentStateNone, false
	}
}

// applyAgentProgress records an OSC 9;4 progress report against a window as an
// AgentSourceOSC claim. It runs on the PTY read goroutine, off the terminal lock
// the VT callback that parked the report was holding.
//
// It goes through ApplyAgentReport, so the ranking decides: an in-band sequence
// outranks the foreground-process detector and the silence timer, and yields to
// the harness reporting for itself. A state the sequence cannot name is ignored
// rather than guessed at.
func (s *Session) applyAgentProgress(windowID string, state vt.ProgressState) {
	s.applyAgentProgressAt(windowID, state, time.Now())
}

// applyAgentProgressAt is applyAgentProgress with the clock passed in, so the
// anti-flicker window is testable without sleeping.
func (s *Session) applyAgentProgressAt(windowID string, state vt.ProgressState, now time.Time) {
	agent, ok := agentStateForProgress(state)
	if !ok {
		return
	}
	current, exists := s.windowAgentState(windowID)
	if !exists {
		return
	}
	// A harness that clears its progress bar between two steps of one task would
	// otherwise blink the pane through idle and back, so a quieter state waits to
	// see whether it stays true.
	if !s.holdQuieterState(windowID, agent, current, AgentSourceOSC, now) {
		return
	}
	// A refused report is the ordinary case when a harness reports for itself, so
	// the error is not worth surfacing; ApplyAgentReport reports refusal as a
	// non-error anyway and the only real error is an unknown window.
	_, _, _ = s.ApplyAgentReport(windowID, AgentReport{State: agent, Source: AgentSourceOSC})
}
