package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Which side runs a hook is now a decision, so it is pinned here.
//
// A hook fires from the side that owns the fact. The window set, the focused
// window, the current workspace and a pane's agent state are the daemon's, so it
// fires them and a client attached to a daemon session does not. That is what
// makes three attached clients produce one firing rather than three, and it is
// what makes the same hook fire when nobody is attached at all. A standalone
// tuios has no daemon, so it fires everything itself.

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

// TestTheDockStillHearsAHookTheDaemonRuns keeps the two things a person wires
// to "when X happens" wired to the same X. The command moved to the daemon; the
// dock component watching the same event is drawn here and has to refresh here.
func TestTheDockStillHearsAHookTheDaemonRuns(t *testing.T) {
	m, _ := sideRig(t, true)
	m.dockEngine = newDockEngine([]*dockComponent{{
		Name:    "custom/windows",
		Command: "echo counted",
		Refresh: config.DockRefresh{
			Kind:   config.DockRefreshEvent,
			Events: []string{string(hooks.AfterNewWindow)},
		},
	}})
	t.Cleanup(m.dockEngine.Stop)

	m.FireHookContext(hooks.AfterNewWindow, hooks.Context{})

	select {
	case u := <-m.dockEngine.Updates():
		if u.Text != "counted" {
			t.Fatalf("the component refreshed to %q", u.Text)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the dock was not told about an event the daemon ran the command for")
	}
}

// TestTheClientListsOnlyTheHooksItFires keeps list-hooks honest about the
// split. A daemon session's client still holds the session-side commands in its
// table, and a row for one would show no runs, which reads as "your hook never
// fired" when the daemon fired it.
func TestTheClientListsOnlyTheHooksItFires(t *testing.T) {
	m, _ := sideRig(t, true)

	rows := m.HookRows()
	if len(rows) == 0 {
		t.Fatal("the client reported no hooks at all")
	}
	for _, row := range rows {
		event := hooks.Event(row["event"].(string))
		if sessionSideHooks[event] {
			t.Errorf("the client listed %s, which the daemon runs", event)
		}
		if row["side"] != "client" {
			t.Errorf("a client row reports side %v", row["side"])
		}
	}

	// A standalone client fires everything, so it lists everything.
	standalone, _ := sideRig(t, false)
	if got, want := len(standalone.HookRows()), len(hooks.AllEvents()); got != want {
		t.Errorf("a standalone client listed %d hooks, want all %d", got, want)
	}
}
