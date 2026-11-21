package session

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"tuios.dev/vt"
)

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

func (s *Session) progressTarget(windowID string) (current AgentState, exists, agentPane bool) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	for i := range s.state.Windows {
		w := &s.state.Windows[i]
		if w.ID != windowID {
			continue
		}
		return w.AgentState, true, s.agentInPane(w)
	}
	return AgentStateNone, false, false
}

func (s *Session) agentInPane(w *WindowState) bool {
	if w.AgentState != AgentStateNone {
		return true
	}
	h := s.agentHarnessPIDs[w.ID]
	return h.pid != 0 && !h.exited()
}

func (s *Session) applyAgentProgress(windowID string, state vt.ProgressState) {
	s.applyAgentProgressAt(windowID, state, time.Now())
}

func (s *Session) applyAgentProgressAt(windowID string, state vt.ProgressState, now time.Time) {
	agent, ok := agentStateForProgress(state)
	if !ok {
		return
	}
	current, exists, agentPane := s.progressTarget(windowID)
	if !exists || !agentPane {
		return
	}
	if !s.holdQuieterState(windowID, agent, current, AgentSourceOSC, now) {
		return
	}
	_, _, _ = s.ApplyAgentReport(windowID, AgentReport{State: agent, Source: AgentSourceOSC})
}
