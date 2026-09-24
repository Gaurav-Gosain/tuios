package session

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
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

// ProgressText is the pane's last OSC 9;4 report as osc_progress rules read
// it, or "" when the pane has sent none. See harness.ProgressText.
func (p *PTY) ProgressText() string {
	v := p.lastProgress.Load()
	if v == 0 {
		return ""
	}
	return harness.ProgressText(int(v>>32)-1, int(int32(uint32(v))))
}

// applyPaneProgress applies an OSC 9;4 report that arrived from a pane. A
// harness whose manifest reads osc_progress, and whose rules have an answer
// for this report, is read by those rules through the ordinary title look,
// since the harness uses the sequence its own way; every other pane gets the
// sequence's published meaning from applyAgentProgress. A rule that has no
// answer for the report leaves it to the published meaning, so a manifest can
// name only the reports it reads differently.
func (s *Session) applyPaneProgress(ptyID, windowID string, state vt.ProgressState, reg *harness.Registry) {
	if reg != nil {
		if _, hid := s.agentHarnessOf(ptyID); hid != "" && reg.HasProgressRules(hid) {
			if pty := s.GetPTY(ptyID); pty != nil {
				if _, _, ok := reg.ClassifyOSC(hid, "", pty.ProgressText()); ok {
					s.scanTitleForAgent(ptyID, reg)
					return
				}
			}
		}
	}
	s.applyAgentProgress(windowID, state)
}

// progressTarget reports a window's agent state, whether the window exists, and
// whether it is known to hold an agent, which is what an OSC 9;4 report needs
// before it may say anything about agent state.
//
// Plenty of programs that are not agents draw a progress bar with the sequence:
// package managers, build tools, downloaders. Read on its own terms it turned a
// plain shell pane into an agent, with a state mark, a silence timer and Inbox
// entries its owner never asked for. So the sequence is believed about an agent
// only once something else has said an agent is there: a state already on the
// pane (a report, a hook, the foreground-process detector, a screen rule), a
// harness named on it, a claim held for it, or a harness process recorded in
// it. The sequence is still parked on the pane either way (see
// storeAgentProgress), so a manifest's osc_progress rules can read it.
func (s *Session) progressTarget(windowID string) (current AgentState, exists, agentPane bool) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	for i := range s.state.Windows {
		w := &s.state.Windows[i]
		if w.ID != windowID {
			continue
		}
		_, claimed := s.agentClaims[w.ID]
		agentPane = w.AgentState != AgentStateNone || w.AgentHarness != "" || claimed || s.agentHarnessPIDs[w.ID] != 0
		return w.AgentState, true, agentPane
	}
	return AgentStateNone, false, false
}

// applyAgentProgress records an OSC 9;4 progress report against a window as an
// AgentSourceOSC claim, when the window is known to hold an agent (see
// progressTarget); on any other pane it does nothing. It runs on the PTY read goroutine, off the terminal lock
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
	current, exists, agentPane := s.progressTarget(windowID)
	if !exists || !agentPane {
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
