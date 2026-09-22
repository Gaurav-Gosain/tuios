package session

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// The pane's window title as an agent-state signal.
//
// tuios has always parsed OSC 0 and OSC 2 and kept the string for the window's
// name, and no tier ever read it for what it says about the agent. The agents
// are already writing to it: Claude Code puts a spinner there while it works,
// Codex writes that an action is required when it blocks. It is the cheapest
// signal in the building, one short string the program chose to publish, and
// it was going in the bin.
//
// It is filed under the OSC source because that is what it is: an escape
// sequence the program emitted about itself, alongside the progress sequence
// already read there. It is not the screen tier, which reads a rendered frame
// the program never meant as a report.
//
// It never creates a claim. A title proves that something set a title, not
// that the something is an agent, and any program can set any string. So a
// pane no other tier has recognised has nothing here to move, which is the
// same gate the screen tier sits behind.

// agentVerdict is what one tier read on a pane: a state, the message to report
// with it, the kind of block the matching rule names, and the source to report
// it as. ok is false when no rule matched. kind is carried into
// AgentReport.Kind, which is what blocked_by reports for needs_input.
type agentVerdict struct {
	ok      bool
	state   AgentState
	message string
	kind    string
	source  AgentSource
}

// titleVerdict matches the harness's title rules against the pane's title.
func titleVerdict(pty *PTY, hid string, reg *harness.Registry) agentVerdict {
	title := pty.Title()
	if title == "" {
		return agentVerdict{}
	}
	state, rule, ok := reg.ClassifyTitle(hid, title)
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

// scanTitleForAgent is the look at the title alone. It reports whether a rule
// matched, on the terms scanPaneForAgent does.
func (s *Session) scanTitleForAgent(ptyID string, reg *harness.Registry) bool {
	return s.lookAtPane(ptyID, reg, true, false)
}

// scanPaneForAgent is the look the daemon takes at a pane that has gone quiet:
// the title first, then the screen.
//
// Both run rather than the first match winning. They answer different
// questions and the ranking decides between them anyway, so stopping at the
// title would mean a pane whose title is stale never had its screen read. It
// reports whether either found something, which is the question the silence
// timer asks: a pane with a rule matching is not idle, whoever owns its claim.
func (s *Session) scanPaneForAgent(ptyID string, reg *harness.Registry) bool {
	return s.lookAtPane(ptyID, reg, true, true)
}

// lookAtPane reads the tiers it is asked to and applies what they say.
//
// An idle reading from one tier gives way to a louder reading from the other.
// A rest glyph in the title says nothing about the permission prompt painted
// under it, and an empty prompt box on the screen says nothing about the
// spinner in the title, so idle is only reported when no tier sees anything
// louder. Every idle reading then goes through the confirmation gate in
// agent_idle.go before it is published.
//
// The result is whether the look found an answer the silence timer must
// respect: any rule matching, except an idle reading a stronger claim would
// refuse anyway, since that pane is still owned by whatever said it was
// working and the timer is how that claim is retired.
func (s *Session) lookAtPane(ptyID string, reg *harness.Registry, useTitle, useScreen bool) bool {
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
	if useTitle {
		title = titleVerdict(pty, hid, reg)
	}
	screenLooked := false
	if useScreen {
		screen, screenLooked = screenVerdict(pty, hid, reg)
		if screenLooked && !screen.ok {
			// Nothing on the screen now, so any claim a blocker or an idle
			// box took here is given back. This look is the only thing that
			// runs when the prompt goes away, and a prompt can only go away
			// by being painted over, which is what brought us here.
			s.releaseAgentBlockerOverride(winID)
			s.releaseScreenIdle(winID)
		}
	}

	if title.ok && title.state == AgentStateIdle && screen.ok && screen.state != AgentStateIdle {
		title.ok = false
	}
	if screen.ok && screen.state == AgentStateIdle && title.ok && title.state != AgentStateIdle {
		screen.ok = false
	}
	// Both idle: one reading is enough, and the title's ranks higher.
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
			if s.applyIdleVerdict(winID, ptyID, reg, v, hid, wrote) {
				matched = true
			}
			continue
		}
		matched = true
		s.ApplyAgentReport(winID, AgentReport{
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

// applyIdleVerdict puts an idle reading through the confirmation gate and
// publishes it once confirmed. It reports whether the reading counts: false
// when a stronger claim owns the window, since publishing would be refused.
func (s *Session) applyIdleVerdict(winID, ptyID string, reg *harness.Registry, v agentVerdict, hid string, wrote int64) bool {
	claim, held := s.agentClaimHeld(winID)
	if held && claim.source.rank() > v.source.rank() {
		s.idle.cancel(winID)
		return false
	}
	current, exists := s.windowAgentState(winID)
	if !exists {
		return false
	}
	// Already idle on this tier's word: saying it again would restamp the
	// state and push it to every client on each keystroke typed into the
	// prompt box.
	if current == AgentStateIdle && held && claim.source == v.source {
		s.idle.cancel(winID)
		return true
	}
	publish, recheck := s.idle.admit(winID, current, time.Now())
	if !publish {
		if recheck > 0 {
			s.idle.schedule(winID, recheck, func() { s.scanPaneForAgent(ptyID, reg) })
		}
		return true
	}
	s.ApplyAgentReport(winID, AgentReport{
		State:       AgentStateIdle,
		Message:     v.message,
		Source:      v.source,
		Harness:     hid,
		paneWroteAt: wrote,
	})
	return true
}

// agentClaimHeld returns the claim on a window and whether one is held.
func (s *Session) agentClaimHeld(windowID string) (agentClaim, bool) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	c, ok := s.agentClaims[windowID]
	return c, ok
}
