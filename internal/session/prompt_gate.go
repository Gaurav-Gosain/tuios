package session

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

const promptStallDefault = 5 * time.Second

const promptGatePoll = 50 * time.Millisecond

func (d *Daemon) promptStall() time.Duration {
	if d.promptStallOverride > 0 {
		return d.promptStallOverride
	}
	return promptStallDefault
}

type promptGate struct {
	windowID     string
	outputCounts bool
	startSeq     uint64
	startState   AgentState
	submittedAt  int64
	pty          *PTY
	reg          *harness.Registry
	harness      string
	late         bool
	taken        string
}

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
	hid := firstNonEmpty(w.AgentHarness, sess.agentClaimFor(w.ID).harness)
	if reg := d.agentMatcher.registry; reg.HasBoxRule(hid) && reg.CanProveWorking(hid) {
		if pty, err := d.resolvePTYForTarget(sess, windowID); err == nil {
			g.pty, g.reg, g.harness = pty, reg, hid
		}
	}
	return g
}

func (g *promptGate) screenShowsTaken() bool {
	if g.pty == nil {
		return false
	}
	if v, _ := screenVerdict(g.pty, g.harness, g.reg); !v.ok || v.state != AgentStateWorking {
		return false
	}
	tail, shown := g.pty.tailShown(g.reg.ScreenLines(g.harness))
	draft, known := g.reg.BoxDraft(g.harness, tail, shown)
	return known && !draft
}

func (d *Daemon) outputShowsTaking(sess *Session, w WindowState) bool {
	harnessID := firstNonEmpty(w.AgentHarness, sess.agentClaimFor(w.ID).harness)
	reg := d.agentMatcher.registry
	return reg == nil || harnessID == "" || !reg.CanProveWorking(harnessID)
}

func (g *promptGate) markSubmitted(at time.Time) {
	g.submittedAt = at.UnixNano()
}

func (g *promptGate) observe(ev streamEvent) {
	if g.taken != "" || ev.Type != EventAgentState || ev.Window != g.windowID || ev.Time < g.submittedAt {
		return
	}
	switch ev.State {
	case AgentStateWorking.Name():
		g.taken = ev.State
	case AgentStateDone.Name():
		if !g.late {
			g.taken = ev.State
		}
	case AgentStateNeedsInput.Name():
		if !g.late && g.startState != AgentStateNeedsInput {
			g.taken = ev.State
		}
	}
}

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
		case g.late:
		case w.AgentState == AgentStateNeedsInput && g.startState != AgentStateNeedsInput:
			g.taken = w.AgentState.Name()
		case w.AgentState == AgentStateDone && g.startState != AgentStateDone:
			g.taken = w.AgentState.Name()
		case w.CompletionSeq > g.startSeq:
			g.taken = "turn"
		}
	}
	if g.taken == "" && g.screenShowsTaken() {
		g.taken = "screen"
	}
	if g.taken == "" && !g.late && g.outputCounts && last != nil && last() > g.submittedAt {
		g.taken = "output"
	}
	return g.taken != ""
}

func (g *promptGate) stalledMessage(stall time.Duration) string {
	what := "it did not turn working or needs_input"
	if g.outputCounts {
		what = "it did not turn working or needs_input and printed nothing"
	}
	return "the prompt was pasted and Enter was sent, and within " + stall.String() + " " + what
}

func (d *Daemon) waitPromptTaken(sess *Session, pty *PTY, g *promptGate, stall time.Duration) (taken, gone bool) {
	taken, gone, _ = d.watchPromptGate(sess, pty, g, stall, promptGatePoll, nil)
	return taken, gone
}

func (d *Daemon) watchPromptGate(sess *Session, pty *PTY, g *promptGate, stall, poll time.Duration, stop <-chan struct{}) (taken, gone, stopped bool) {
	sub := d.events.subscribe(eventFilter{
		session: sess.Name(),
		sess:    sess,
		types:   map[string]bool{EventAgentState: true, EventWindowClosed: true, EventSessionClosed: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	deadline := time.NewTimer(stall)
	defer deadline.Stop()
	tick := time.NewTicker(poll)
	defer tick.Stop()
	look := true
	for {
		if look && g.check(sess, pty.LastOutput) {
			return true, false, false
		}
		look = true
		select {
		case <-deadline.C:
			return g.check(sess, pty.LastOutput), false, false
		case <-d.ctx.Done():
			return false, false, false
		case <-stop:
			return false, false, true
		case ev := <-sub.ch:
			if ev.Type == EventSessionClosed || (ev.Type == EventWindowClosed && ev.Window == g.windowID) {
				return false, true, false
			}
			g.observe(ev)
			look = ev.Window == g.windowID
		case <-tick.C:
		}
	}
}
