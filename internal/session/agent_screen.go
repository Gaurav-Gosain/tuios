package session

import (
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

const screenScanInterval = 250 * time.Millisecond

const screenSettleDelay = 400 * time.Millisecond

func screenVerdict(pty *PTY, hid string, reg *harness.Registry) (v agentVerdict, looked bool) {
	lines := reg.ScreenLines(hid)
	if lines <= 0 {
		return agentVerdict{}, false
	}
	tail := pty.tailText(lines)
	if len(tail) == 0 {
		return agentVerdict{}, false
	}
	state, rule, ok := reg.Classify(hid, tail)
	if !ok {
		return agentVerdict{}, true
	}
	msg, kind := screenRuleMessage(reg, hid, rule, tail), reg.RuleKind(hid, rule)
	if AgentState(state) == AgentStateLimitReached {
		kind = ""
		line := strings.TrimLeft(reg.RulePrompt(hid, rule, tail), "⎿ ")
		if line == "" {
			line = reg.RuleMessage(hid, rule)
		}
		msg = harness.LimitMessage(line, reg.RuleLimitReset(hid, rule, tail))
	}
	return agentVerdict{
		ok:      true,
		state:   AgentState(state),
		message: msg,
		kind:    kind,
		source:  AgentSourceScreen,
	}, true
}

func promptBoxEmpty(pty *PTY, hid string, reg *harness.Registry) bool {
	if !reg.HasBoxRule(hid) {
		return true
	}
	if pty == nil {
		return false
	}
	tail, shown := pty.tailShown(reg.ScreenLines(hid))
	draft, known := reg.BoxDraft(hid, tail, shown)
	return known && !draft
}

func (s *Session) releaseScreenIdle(windowID string) {
	if claim, held := s.agentClaimHeld(windowID); !held || claim.source != AgentSourceScreen {
		return
	}
	if state, _ := s.windowAgentState(windowID); state != AgentStateIdle {
		return
	}
	s.yieldAgentClaim(windowID, AgentSourceScreen)
}

func screenRuleMessage(reg *harness.Registry, hid string, rule int, tail []string) string {
	if prompt := reg.RulePrompt(hid, rule, tail); prompt != "" {
		if kind := reg.RuleKind(hid, rule); kind != "" && !(reg.RuleShowsDialog(hid, rule) && strings.HasPrefix(prompt, "approve ")) {
			return kind + ": " + prompt
		}
		return prompt
	}
	return reg.RuleMessage(hid, rule)
}

func (s *Session) agentHarnessOf(ptyID string) (windowID, harnessID string) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	for i := range s.state.Windows {
		if w := &s.state.Windows[i]; w.PTYID == ptyID {
			if !s.agentInPane(w) {
				return w.ID, ""
			}
			return w.ID, w.AgentHarness
		}
	}
	return "", ""
}

func (p *PTY) tailText(lines int) []string {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	if p.terminal == nil {
		return nil
	}
	return p.terminal.TailText(lines)
}

func (p *PTY) screenScanDue(now int64) bool {
	if now-p.lastScreenScan.Load() < int64(screenScanInterval) {
		return false
	}
	p.lastScreenScan.Store(now)
	return true
}

func (p *PTY) hasScreenLook() bool {
	p.screenSettleMu.Lock()
	defer p.screenSettleMu.Unlock()
	return p.screenLook != nil
}

func (p *PTY) setScreenLook(f func()) {
	p.screenSettleMu.Lock()
	defer p.screenSettleMu.Unlock()
	p.screenLook = f
}

func (p *PTY) armScreenSettle() {
	p.screenSettleMu.Lock()
	defer p.screenSettleMu.Unlock()
	if p.screenLook == nil {
		return
	}
	if p.screenSettle == nil {
		p.screenSettle = time.AfterFunc(screenSettleDelay, p.runScreenLook)
		return
	}
	p.screenSettle.Reset(screenSettleDelay)
}

func (p *PTY) runScreenLook() {
	p.screenSettleMu.Lock()
	f := p.screenLook
	p.screenSettleMu.Unlock()
	if f != nil {
		f()
	}
}

func (p *PTY) stopScreenSettle() {
	p.screenSettleMu.Lock()
	defer p.screenSettleMu.Unlock()
	if p.screenSettle != nil {
		p.screenSettle.Stop()
		p.screenSettle = nil
	}
}

func (s *Session) noteAgentWaitsOn(windowID, waits string) {
	s.stateMu.RLock()
	same := false
	if w, ok := findWindowState(s.state, windowID); ok {
		same = w.AgentWaitsOn == waits
	}
	s.stateMu.RUnlock()
	if same {
		return
	}
	_ = s.mutateState(func(st *SessionState) error {
		i, err := findWindowStateIndex(st.Windows, windowID)
		if err != nil || st.Windows[i].AgentWaitsOn == waits {
			return errNoTaskMove
		}
		st.Windows[i].AgentWaitsOn = waits
		return nil
	})
}

func agentWaitsOn(w WindowState) string {
	switch w.ShownAgentState() {
	case AgentStateDone, AgentStateIdle:
		return w.AgentWaitsOn
	}
	return ""
}
