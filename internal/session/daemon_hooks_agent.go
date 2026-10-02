//go:build !slim

package session

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
)

// fireAgentStateHook applies the [notifications.agent] policy to a transition
// and fires the hook the policy leaves standing.
//
// The policy is the client's, read from the same config table, so a user who
// muted a state stays muted whichever side runs the command. Two of its rules
// are deliberately not applied the same way:
//
//   - suppress_focused asks not to be told about a pane the user is looking at.
//     With no client attached nobody is looking at anything, so it applies only
//     while a client is attached, and only while at least one attached
//     client's terminal has not reported losing focus.
//   - The dock message, the bell and the sound cue stay in the client. They
//     need a terminal and a person. Only the command moves.
//
// Everything after the two pure checks runs off this goroutine, because reading
// the client table or the session back from the sink would take a second lock
// under the state lock the sink already holds.
func (d *Daemon) fireAgentStateHook(sess *Session, ev SessionEvent) {
	policy := d.agentAlerts
	key := sess.Name() + "\x00" + ev.Window

	// Any further transition retires whatever was parked for this pane: the
	// state it was going to announce is no longer the state the pane is in.
	d.agentHooks.cancel(key)

	if !policy.Alerts(ev.State) {
		return
	}
	if policy.Quiet(time.Now()) {
		return
	}

	sessionID, sessionName, window, to := sess.ID, sess.Name(), ev.Window, ev.State
	ctx := hookContext(sessionName, ev)

	// suppressed reports whether a client is attached and is showing this
	// pane, on a terminal that has not said it lost focus. With every client's
	// terminal out of focus nobody is looking at the pane, whatever it shows.
	suppressed := func() bool {
		if !policy.SuppressFocused || d.findTUIClient(sessionID) == nil {
			return false
		}
		if d.sessionHostFocus(sessionID) == HostFocusUnfocused {
			return false
		}
		live := d.manager.GetSessionByID(sessionID)
		if live == nil {
			return false
		}
		state := live.GetState()
		return state != nil && state.FocusedWindowID == window
	}

	if policy.Settle <= 0 {
		go func() {
			if !suppressed() {
				d.hooks.Fire(hooks.AfterAgentState, ctx)
			}
		}()
		return
	}

	d.agentHooks.park(key, policy.Settle, func() {
		if suppressed() {
			return
		}
		// Re-read rather than trust the parked state: the pane may have closed
		// or moved on while it waited.
		live := d.manager.GetSessionByID(sessionID)
		if live == nil {
			return
		}
		w, ok := findWindowState(live.GetState(), window)
		if !ok || w.AgentState.Name() != to {
			return
		}
		d.hooks.Fire(hooks.AfterAgentState, ctx)
	})
}

// park cancels whatever was waiting for this window and schedules fire after d.
func (g *agentHookGate) park(key string, d time.Duration, fire func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if t, ok := g.pending[key]; ok {
		t.Stop()
		delete(g.pending, key)
	}
	g.pending[key] = time.AfterFunc(d, func() {
		g.mu.Lock()
		delete(g.pending, key)
		g.mu.Unlock()
		fire()
	})
}

// cancel drops a parked firing without running it.
func (g *agentHookGate) cancel(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if t, ok := g.pending[key]; ok {
		t.Stop()
		delete(g.pending, key)
	}
}
