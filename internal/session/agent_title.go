package session

import "github.com/Gaurav-Gosain/tuios/internal/harness"

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
func (s *Session) scanTitleForAgent(ptyID string, reg *harness.Registry) bool {
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
	title := pty.Title()
	if title == "" {
		return false
	}
	state, rule, ok := reg.ClassifyTitle(hid, title)
	if !ok {
		return false
	}
	s.ApplyAgentReport(winID, AgentReport{
		State:       AgentState(state),
		Message:     reg.TitleRuleMessage(hid, rule),
		Source:      AgentSourceOSC,
		Harness:     hid,
		paneWroteAt: pty.LastOutput(),
	})
	return true
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
	titled := s.scanTitleForAgent(ptyID, reg)
	scanned := s.scanScreenForAgent(ptyID, reg)
	return titled || scanned
}
