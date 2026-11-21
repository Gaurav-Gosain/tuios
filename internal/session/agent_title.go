package session

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

type agentVerdict struct {
	ok      bool
	state   AgentState
	message string
	kind    string
	source  AgentSource
}

func titleVerdict(pty *PTY, hid string, reg *harness.Registry) agentVerdict {
	title := pty.Title()
	progress := ""
	if reg.HasProgressRules(hid) {
		progress = pty.ProgressText()
	}
	if title == "" && progress == "" {
		return agentVerdict{}
	}
	state, rule, ok := reg.ClassifyOSC(hid, title, progress)
	if !ok {
		return agentVerdict{}
	}
	return agentVerdict{
		ok:      true,
		state:   AgentState(state),
		message: reg.TitleRuleMessage(hid, rule),
		kind:    reg.TitleRuleKind(hid, rule),
		source:  AgentSourceOSC,
	}
}

func (s *Session) scanTitleForAgent(ptyID string, reg *harness.Registry) bool {
	return s.lookAtPane(ptyID, reg, paneLook{title: true})
}

type paneLook struct {
	title, screen bool
	stalled       bool
}

func (s *Session) scanPaneForAgent(ptyID string, reg *harness.Registry) bool {
	return s.lookAtPane(ptyID, reg, paneLook{title: true, screen: true})
}

func (s *Session) scanStalledPane(ptyID string, reg *harness.Registry) bool {
	return s.lookAtPane(ptyID, reg, paneLook{title: true, screen: true, stalled: true})
}

func (s *Session) lookAtPane(ptyID string, reg *harness.Registry, look paneLook) bool {
	if reg == nil {
		return false
	}
	pty := s.GetPTY(ptyID)
	if pty == nil {
		return false
	}
	winID, hid := s.agentHarnessOf(ptyID)
	if hid == "" {
		return false
	}
	s.idle.noteHarnessSeen(winID, hid, time.Now())

	var title, screen agentVerdict
	if look.title {
		title = titleVerdict(pty, hid, reg)
		if look.stalled && title.ok && title.state == AgentStateWorking {
			title.ok = false
		}
	}
	screenLooked := false
	if look.screen {
		s.noteAgentWaitsOn(winID, reg.WaitsOn(hid, pty.tailText(reg.ScreenLines(hid))))
		screen, screenLooked = screenVerdict(pty, hid, reg)
		if screenLooked && !screen.ok {
			s.releaseAgentBlockerOverride(winID)
			s.releaseScreenIdle(winID)
		}
	}

	screenIdle := screen.ok && screen.state == AgentStateIdle
	if title.ok && title.state == AgentStateIdle && screen.ok && screen.state != AgentStateIdle {
		title.ok = false
	}
	if title.ok && title.state == AgentStateWorking && screen.ok && agentStateBlocks(screen.state) {
		title.ok = false
	}
	if screen.ok && screen.state == AgentStateIdle && title.ok && title.state != AgentStateIdle {
		screen.ok = false
	}
	if title.ok && screen.ok && title.state == AgentStateIdle && screen.state == AgentStateIdle {
		screen.ok = false
	}

	wrote := pty.LastOutput()
	matched := false
	idleSeen := false
	for _, v := range []agentVerdict{title, screen} {
		if !v.ok {
			continue
		}
		if v.state == AgentStateIdle {
			idleSeen = true
			if s.applyIdleVerdict(winID, ptyID, reg, v, hid, wrote, screenIdle) {
				matched = true
			}
			continue
		}
		matched = true
		_, _, _ = s.ApplyAgentReport(winID, AgentReport{
			State:       v.state,
			Message:     v.message,
			Kind:        v.kind,
			Source:      v.source,
			Harness:     hid,
			paneWroteAt: wrote,
		})
	}
	if !idleSeen {
		s.idle.cancel(winID)
	}
	return matched
}

func (s *Session) applyIdleVerdict(winID, ptyID string, reg *harness.Registry, v agentVerdict, hid string, wrote int64, screenIdle bool) bool {
	claim, held := s.agentClaimHeld(winID)
	current, exists := s.windowAgentState(winID)
	if !exists {
		return false
	}
	hookPrompt := held && current == AgentStateNeedsInput && claim.hookEventPrompt()
	if hookPrompt && !(screenIdle && promptBoxEmpty(s.GetPTY(ptyID), hid, reg)) {
		s.idle.cancel(winID)
		return false
	}
	if held && claim.source.Rank() > v.source.Rank() && !s.eventClaimWrittenOver(winID, claim, wrote) {
		s.idle.cancel(winID)
		return false
	}
	if current == AgentStateIdle && held && claim.source == v.source {
		s.idle.cancel(winID)
		return true
	}
	gate := current
	if hookPrompt {
		gate = AgentStateWorking
	}
	publish, recheck := s.idle.admit(winID, gate, time.Now())
	if !publish {
		if recheck > 0 {
			s.idle.schedule(winID, recheck, func() { s.scanPaneForAgent(ptyID, reg) })
		}
		return true
	}
	_, _, _ = s.ApplyAgentReport(winID, AgentReport{
		State:       AgentStateIdle,
		Message:     v.message,
		Source:      v.source,
		Harness:     hid,
		paneWroteAt: wrote,
		promptGone:  hookPrompt,
	})
	return true
}

func (s *Session) agentClaimHeld(windowID string) (agentClaim, bool) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	c, ok := s.agentClaims[windowID]
	return c, ok
}
