package session

import (
	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// Desktop notifications from a pane, read as agent state.
//
// A harness that wants its user sends a desktop notification over OSC 9, OSC
// 777 or OSC 99. Both emulators parsed those and the daemon threw them away:
// only an attached client ever saw one, as a toast, so a harness asking for
// approval in a session nobody was attached to asked nobody. Now the daemon
// reads them too. Every one is published on the event stream as a
// notification event, and one from a pane attributed to a harness is matched
// against that harness's [notify] rules and, when a rule matches, becomes the
// pane's agent state. The state change is what raises the rail, the alert and
// the after-agent-state hook, the same way any other source's would.
//
// A notification is filed under the OSC source, beside the title and the
// progress sequence: it is an escape sequence the program sent about itself.
// It is also an event rather than a standing fact, so its claim goes stale the
// moment the pane paints anything after it (see AgentReport.event).

// notifyTextCap bounds the notification text an event carries. A notification
// is a sentence; one carrying a screenful is not worth fanning out.
const notifyTextCap = 512

// paneNotification is one parked notification.
type paneNotification struct {
	title string
	body  string
}

// storeAgentNotify parks a notification for the read goroutine to apply.
// Called from the VT callback under the terminal lock, so it is one atomic
// store. A burst between two output events collapses to the newest.
func (p *PTY) storeAgentNotify(title, body string) {
	p.agentNotify.Store(&paneNotification{title: title, body: body})
}

// takeAgentNotify returns the parked notification and clears it.
func (p *PTY) takeAgentNotify() (paneNotification, bool) {
	n := p.agentNotify.Swap(nil)
	if n == nil {
		return paneNotification{}, false
	}
	return *n, true
}

// capNotifyText trims a notification field to notifyTextCap bytes on a rune
// boundary.
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

// runeStart reports whether b begins a UTF-8 sequence.
func runeStart(b byte) bool { return b&0xC0 != 0x80 }

// applyAgentNotify matches a notification against the harness running in the
// pane and records what the matching rule says. It runs on the PTY read
// goroutine, off the terminal lock. A pane no tier has attributed to a harness
// has no rules to match, which is the gate the title tier sits behind too.
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
	// A refusal (outranked, unchanged, a claim held) is the normal outcome of
	// a passive scan, and the scan has nothing to report it to.
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
