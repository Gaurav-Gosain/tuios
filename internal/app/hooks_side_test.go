package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestADaemonClientLeavesSessionSideHooksToTheDaemon is the half of the
// multi-client rule that lives in the client. Without it the same window
// creation runs the command once per attached client, on top of the daemon's
// own firing.
func TestADaemonClientLeavesSessionSideHooksToTheDaemon(t *testing.T) {
	m, r := sideRig(t, true)

	for _, e := range hooks.AllEvents() {
		m.FireHookContext(e, hooks.Context{})
	}

	fired := r.firedEvents(m)
	for _, e := range session.SessionSideHookEvents() {
		if fired[e] != 0 {
			t.Errorf("%s fired %d times in a client attached to a daemon session; the daemon runs it",
				e, fired[e])
		}
	}
	// And the client still runs the ones that need its terminal.
	for _, e := range []hooks.Event{hooks.AfterAttach, hooks.AfterDetach, hooks.AfterResize, hooks.AfterLayoutChange} {
		if fired[e] != 1 {
			t.Errorf("%s fired %d times in a daemon client, want 1: only this client can run it", e, fired[e])
		}
	}
}

// sideRig builds a client with every hook registered and a recorder in place of
// a shell.
func sideRig(t *testing.T, daemonSession bool) (*OS, *hookRecorder) {
	t.Helper()
	m := &OS{IsDaemonSession: daemonSession, SessionName: "work", CurrentWorkspace: 1}
	r := record(t, m)
	return m, r
}

// firedEvents lists which events reached the runner.
func (r *hookRecorder) firedEvents(m *OS) map[hooks.Event]int {
	m.HookManager.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[hooks.Event]int{}
	for _, c := range r.fired {
		out[c.EventType]++
	}
	return out
}

// TestAStandaloneClientFiresEveryHookItself keeps the split from breaking the
// mode that has no daemon to defer to.
func TestAStandaloneClientFiresEveryHookItself(t *testing.T) {
	m, r := sideRig(t, false)

	for _, e := range hooks.AllEvents() {
		m.FireHookContext(e, hooks.Context{})
	}

	fired := r.firedEvents(m)
	for _, e := range hooks.AllEvents() {
		if fired[e] != 1 {
			t.Errorf("%s fired %d times in a standalone client, want 1", e, fired[e])
		}
	}
}
