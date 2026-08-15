package session

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// screenScanInterval bounds how often output drives a screen scan, so a pane
// printing a build log pays for one scan per interval rather than one per chunk.
const screenScanInterval = 250 * time.Millisecond

// screenSettleDelay is how long after output stops before the settle scan runs.
//
// This is the whole reason the throttle alone is not enough. The state a rule
// most wants to see is a blocking prompt, and a blocking prompt is painted by
// the LAST chunk a pane emits before it goes silent: the throttle can swallow
// exactly that chunk and then nothing else ever arrives to trigger a scan. One
// timer, armed on output and disarmed when it fires, closes that hole without a
// ticker, so a pane that stays silent costs nothing.
const screenSettleDelay = 400 * time.Millisecond

// scanScreenForAgent matches the harness's screen rules against the bottom of a
// pane and reports what they say.
//
// It is the last-resort tier and reports as AgentSourceScreen, so it can never
// write over a harness reporting for itself or an escape sequence the pane
// emitted. What it can do is see a state those two never mention: a harness
// sitting on a blocking prompt paints it once and then emits nothing at all, no
// title and no progress sequence, so the screen is the only channel carrying the
// fact that a human is being waited for.
//
// It reports whether a rule matched, which is a different question from whether
// the report was accepted: a matching rule means the screen carries an answer
// even when a higher-ranked source owns the window, and the silence timer needs
// that fact to know it must not call the pane idle.
func (s *Session) scanScreenForAgent(ptyID string, reg *harness.Registry) bool {
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
	lines := reg.ScreenLines(hid)
	if lines <= 0 {
		return false
	}

	tail := pty.tailText(lines)
	if len(tail) == 0 {
		return false
	}
	state, _, ok := reg.Classify(hid, tail)
	if !ok {
		return false
	}
	s.ApplyAgentReport(winID, AgentReport{
		State:   AgentState(state),
		Source:  AgentSourceScreen,
		Harness: hid,
	})
	return true
}

// agentHarnessOf names the window backed by ptyID and the harness running in it,
// under the state read lock. An empty harness means nothing has claimed the pane
// as an agent, and a screen rule has nothing to match with.
func (s *Session) agentHarnessOf(ptyID string) (windowID, harnessID string) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	for i := range s.state.Windows {
		if s.state.Windows[i].PTYID == ptyID {
			return s.state.Windows[i].ID, s.state.Windows[i].AgentHarness
		}
	}
	return "", ""
}

// tailText reads the bottom of the pane's emulator under the terminal lock, the
// same lock GetTerminalState takes.
func (p *PTY) tailText(lines int) []string {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	if p.terminal == nil {
		return nil
	}
	return p.terminal.TailText(lines)
}

// screenScanDue reports whether enough time has passed since the last
// output-driven screen scan to run another, and claims the slot if so. Called
// only from the single PTY read goroutine, so a plain load/store is race-free.
func (p *PTY) screenScanDue(now int64) bool {
	if now-p.lastScreenScan.Load() < int64(screenScanInterval) {
		return false
	}
	p.lastScreenScan.Store(now)
	return true
}

// armScreenSettle schedules the one scan that runs after a pane goes quiet.
// Re-arming while output is still flowing pushes the scan out, so a busy pane
// runs it once when it finally stops rather than once per chunk.
func (p *PTY) armScreenSettle(f func()) {
	p.screenSettleMu.Lock()
	defer p.screenSettleMu.Unlock()
	if p.screenSettle != nil {
		p.screenSettle.Stop()
	}
	p.screenSettle = time.AfterFunc(screenSettleDelay, f)
}

// stopScreenSettle disarms the settle timer, for a pane being closed.
func (p *PTY) stopScreenSettle() {
	p.screenSettleMu.Lock()
	defer p.screenSettleMu.Unlock()
	if p.screenSettle != nil {
		p.screenSettle.Stop()
		p.screenSettle = nil
	}
}
