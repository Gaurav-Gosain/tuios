package session

import (
	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

const notifyTextCap = 512

type paneNotification struct {
	title string
	body  string
}

func (p *PTY) storeAgentNotify(title, body string) {
	p.agentNotify.Store(&paneNotification{title: title, body: body})
}

func (p *PTY) takeAgentNotify() (paneNotification, bool) {
	n := p.agentNotify.Swap(nil)
	if n == nil {
		return paneNotification{}, false
	}
	return *n, true
}

func capNotifyText(s string) string {
	if len(s) <= notifyTextCap {
		return s
	}
	cut := notifyTextCap
	for cut > 0 && !runeStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func runeStart(b byte) bool { return b&0xC0 != 0x80 }

func (s *Session) applyAgentNotify(ptyID string, n paneNotification, reg *harness.Registry) bool {
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
	text := harness.NotifyText(n.title, n.body)
	state, rule, ok := reg.ClassifyNotify(hid, text)
	if !ok {
		return false
	}
	if AgentState(state) != AgentStateIdle {
		s.idle.cancel(winID)
	}
	_, _, _ = s.ApplyAgentReport(winID, AgentReport{
		State:       AgentState(state),
		Message:     reg.NotifyRuleMessage(hid, rule, text),
		Kind:        reg.NotifyRuleKind(hid, rule),
		Source:      AgentSourceOSC,
		Harness:     hid,
		paneWroteAt: pty.LastOutput(),
		event:       true,
	})
	return true
}
