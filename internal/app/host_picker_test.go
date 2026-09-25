package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The machine picker is the way to put a window on another machine from inside
// the UI. Until it existed the only way was the command line, which meant the
// feature was invisible to anyone using tuios rather than scripting it.

func pickerOS(t *testing.T) *OS {
	t.Helper()
	m := sidebarTestOS(t, 120, 40, "left")
	m.SessionName = "work"
	m.IsDaemonSession = true
	// A client object, not a connection: the gates being tested ask whether
	// this is a daemon session at all, and every path that would use it is
	// guarded again where it is used.
	m.DaemonClient = session.NewTUIClient()
	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 2,
		Snapshot: FederationSnapshot{Hosts: []FederationHost{
			{Name: federation.LocalHostName, Status: string(federation.StatusUp)},
			{Name: "build", Status: string(federation.StatusUp), Sessions: []FederationSession{{Name: "api"}}},
			{Name: "workstation", Status: string(federation.StatusUnreachable)},
		}},
	})
	return m
}

// TestChoosingAnotherMachineIsNotDoneOnTheUIGoroutine.
//
// Opening a pane elsewhere dials a stream on the link and waits for that
// daemon to spawn a process. Doing it inline would freeze every pane on screen
// for as long as it took, which on a machine that has gone away is the full
// budget.
//
// Negative control: calling the verb inline and returning nil fails here.
func TestChoosingAnotherMachineIsNotDoneOnTheUIGoroutine(t *testing.T) {
	m := pickerOS(t)
	cmd := m.ChooseHostForNewWindow(HostPickerItem{Name: "build", Label: "build", Up: true})
	if cmd == nil {
		t.Fatal("choosing another machine did no work off the UI goroutine")
	}
}
