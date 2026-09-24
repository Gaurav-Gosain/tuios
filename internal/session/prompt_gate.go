package session

import (
	"time"
)

// The stall gate: did the agent take the prompt it was just given?
//
// submitPrompt pastes a prompt and presses Enter, and what it cannot know is
// whether the application read that Enter as "submit". A TUI still starting
// drops keys, a TUI that detects a paste by timing turns an Enter inside the
// burst into a newline, and a TUI with a different submit key leaves the text in
// its input box. In every one of those cases the prompt sits there unsent, and
// until now ask-agent waited out its settle time and returned an empty reply as
// if the agent had nothing to say, and fan reported the prompt as sent.
//
// So after the Enter the pane has a few seconds to show that it took the
// prompt, which is what herdr calls the agent_prompt_stalled check:
//
//   - its agent state turns working, or needs_input when it was not on
//     needs_input before, or done;
//   - it finishes a turn, which CompletionSeq counts, for an agent quick enough
//     to go working and back to rest between two looks;
//   - or it prints something after the Enter, but only for a pane whose harness
//     has no rule that shows working. For a harness that can show working,
//     output alone is not evidence: a TUI that read the Enter as a newline also
//     redraws its input box, and that is exactly the stall this gate is for.
//
// A pane that shows none of these in time is reported as stalled: ask-agent
// fails with prompt_stalled, and fan records prompt_status stalled. Nothing is
// typed again, because the prompt may still be sitting in the input box and a
// second copy would be worse than the first being late.

// promptStallDefault is how long a pane has after the Enter to show it took
// the prompt. herdr uses the same five seconds.
const promptStallDefault = 5 * time.Second

// promptGatePoll is how often the gate looks at the pane between events.
const promptGatePoll = 50 * time.Millisecond

// promptStall is the daemon's stall window: promptStallDefault unless a test set
// promptStallOverride.
func (d *Daemon) promptStall() time.Duration {
	if d.promptStallOverride > 0 {
		return d.promptStallOverride
	}
	return promptStallDefault
}

// promptGate is what the stall gate knows about one pane: how it looked before
// the prompt was typed, and whether it has shown since that it took it.
type promptGate struct {
	windowID string
	// outputCounts says new output is evidence. It is false for a harness
	// whose rules can show working; see the file comment.
	outputCounts bool
	// startSeq and startState are the pane's CompletionSeq and agent state
	// before the prompt was typed.
	startSeq   uint64
	startState AgentState
	// submittedAt is when the Enter was sent, in Unix nanoseconds.
	submittedAt int64
	// taken names what showed the prompt was taken: a state name, "turn" for a
	// finished turn, or "output". Empty until something did.
	taken string
}

// newPromptGate reads the pane before anything is typed at it.
func (d *Daemon) newPromptGate(sess *Session, windowID string) *promptGate {
	g := &promptGate{windowID: windowID, outputCounts: true}
	st := sess.GetState()
	i, err := findWindowStateIndex(st.Windows, windowID)
	if err != nil {
		return g
	}
	w := st.Windows[i]
	g.startSeq = w.CompletionSeq
	g.startState = w.AgentState
	g.outputCounts = d.outputShowsTaking(sess, w)
	return g
}

// outputShowsTaking reports whether output from the pane counts as evidence
// that its agent took a prompt: true unless its harness has a rule that shows
// working. See the file comment.
func (d *Daemon) outputShowsTaking(sess *Session, w WindowState) bool {
	harnessID := firstNonEmpty(w.AgentHarness, sess.agentClaimFor(w.ID).harness)
	reg := d.agentMatcher.registry
	return reg == nil || harnessID == "" || !reg.CanProveWorking(harnessID)
}

// markSubmitted records when the Enter went out.
func (g *promptGate) markSubmitted(at time.Time) {
	g.submittedAt = at.UnixNano()
}

// observe takes an agent-state event for the pane. It is how a state that came
// and went between two polls is still seen.
func (g *promptGate) observe(ev streamEvent) {
	if g.taken != "" || ev.Type != EventAgentState || ev.Window != g.windowID || ev.Time < g.submittedAt {
		return
	}
	switch ev.State {
	case AgentStateWorking.Name(), AgentStateDone.Name():
		g.taken = ev.State
	case AgentStateNeedsInput.Name():
		if g.startState != AgentStateNeedsInput {
			g.taken = ev.State
		}
	}
}

// check looks at the pane now and reports whether it has shown that it took
// the prompt. last is the pane's output clock.
func (g *promptGate) check(sess *Session, last func() int64) bool {
	if g.taken != "" {
		return true
	}
	st := sess.GetState()
	if i, err := findWindowStateIndex(st.Windows, g.windowID); err == nil {
		w := st.Windows[i]
		switch {
		case w.AgentState == AgentStateWorking:
			g.taken = w.AgentState.Name()
		case w.AgentState == AgentStateNeedsInput && g.startState != AgentStateNeedsInput:
			g.taken = w.AgentState.Name()
		case w.AgentState == AgentStateDone && g.startState != AgentStateDone:
			g.taken = w.AgentState.Name()
		case w.CompletionSeq > g.startSeq:
			g.taken = "turn"
		}
	}
	if g.taken == "" && g.outputCounts && last != nil && last() > g.submittedAt {
		g.taken = "output"
	}
	return g.taken != ""
}

// stalledMessage says what the pane did not do, for the error and the note.
func (g *promptGate) stalledMessage(stall time.Duration) string {
	what := "it did not turn working or needs_input"
	if g.outputCounts {
		what = "it did not turn working or needs_input and printed nothing"
	}
	return "the prompt was pasted and Enter was sent, and within " + stall.String() + " " + what
}

// waitPromptTaken polls the gate until the pane shows it took the prompt, the
// stall window ends, the window closes, or the daemon stops. It reports whether
// the prompt was taken and whether the window is gone.
func (d *Daemon) waitPromptTaken(sess *Session, pty *PTY, g *promptGate, stall time.Duration) (taken, gone bool) {
	sub := d.events.subscribe(eventFilter{
		session: sess.Name,
		types:   map[string]bool{EventAgentState: true, EventWindowClosed: true, EventSessionClosed: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	deadline := time.NewTimer(stall)
	defer deadline.Stop()
	tick := time.NewTicker(promptGatePoll)
	defer tick.Stop()
	for {
		if g.check(sess, pty.LastOutput) {
			return true, false
		}
		select {
		case <-deadline.C:
			return g.check(sess, pty.LastOutput), false
		case <-d.ctx.Done():
			return false, false
		case ev := <-sub.ch:
			if ev.Type == EventSessionClosed || (ev.Type == EventWindowClosed && ev.Window == g.windowID) {
				return false, true
			}
			g.observe(ev)
		case <-tick.C:
		}
	}
}
