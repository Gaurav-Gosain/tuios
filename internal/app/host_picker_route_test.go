package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Where a pick in the machine picker goes. The rows name machines the way this
// machine's daemon knows them, and each pick has to reach the daemon that can
// act on it. The e2e tests in global_new_window_test.go and
// global_remote_pick_test.go prove the same rules against real daemons.

// notified reports whether any notification on screen says text.
func notified(m *OS, text string) bool {
	for _, n := range m.Notifications {
		if strings.Contains(n.Message, text) {
			return true
		}
	}
	return false
}

// TestAClickInTheSessionPickerMakesASession.
//
// Enter and a click reach the same row, so they must do the same thing. The
// click used to make a window whatever the picker was open for.
//
// Negative control: hostPickerActivate calling ChooseHostForNewWindow again
// returns a window command and says nothing, and fails here.
func TestAClickInTheSessionPickerMakesASession(t *testing.T) {
	m := pickerOS(t)
	m.OpenNewSessionPicker()
	if !m.ShowHostPicker || m.HostPickerPurpose != HostPickerNewSession {
		t.Fatal("the session picker did not open")
	}
	build := -1
	for i, it := range FilterHostPickerItems(m.HostPickerItems, "") {
		if it.Name == "build" {
			build = i
		}
	}
	if build < 0 {
		t.Fatal("the fixture's machine is not listed")
	}
	// Making a session on build connects for real. With no client the
	// attempt fails at once and says so, which proves the session path ran.
	m.DaemonClient = nil
	if cmd := m.hostPickerActivate(build); cmd != nil {
		t.Fatal("ASSERTION: a click in the session picker returned a window command")
	}
	if m.ShowHostPicker {
		t.Error("the picker stayed open after a click")
	}
	if !notified(m, "not in daemon mode") {
		t.Fatalf("ASSERTION: the click did not try to make a session on build: %+v", m.Notifications)
	}
}

// TestFromAnotherMachineThisMachineIsNotAWindowTarget.
//
// While the client is attached to a session on build, a window is made by
// build's daemon, which has no link back here that this client knows of. The
// row for this machine used to make a pane on build.
//
// Negative control: dropping the attached check in ChooseHostForNewWindow
// asks the attached daemon for a window and fails here.
func TestFromAnotherMachineThisMachineIsNotAWindowTarget(t *testing.T) {
	m := pickerOS(t)
	m.AttachedHost = "build"
	m.HostPickerPurpose = HostPickerNewWindow
	items := m.buildHostPickerItems()
	if items[0].Name != "" || items[0].Up {
		t.Fatalf("ASSERTION: this machine is offered as a window target from build: %+v", items[0])
	}
	if cmd := m.ChooseHostForNewWindow(items[0]); cmd != nil || m.daemonWindowIntent {
		t.Fatal("ASSERTION: a pick of this machine from build made a window")
	}
	if !notified(m, "build cannot open a pane on this machine") {
		t.Fatalf("the pick did not say why: %+v", m.Notifications)
	}

	// A session is still made here: the session picker keeps the row.
	m.HostPickerPurpose = HostPickerNewSession
	if items := m.buildHostPickerItems(); !items[0].Up {
		t.Error("ASSERTION: the session picker refuses this machine from build")
	}
}

// TestAPickIsCheckedAgainstTheLinkAsItIsNow.
//
// The list is a snapshot from when the picker opened. A machine whose link
// dropped since then still has a row that says up, and a pick dialled it and
// came back with the link's own state word.
//
// Negative control: checking item.Up alone returns a command here.
func TestAPickIsCheckedAgainstTheLinkAsItIsNow(t *testing.T) {
	m := pickerOS(t)
	stale := HostPickerItem{Name: "build", Label: "build", Up: true}
	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 2,
		Snapshot: FederationSnapshot{Hosts: []FederationHost{
			{Name: federation.LocalHostName, Status: string(federation.StatusUp)},
			{Name: "build", Status: string(federation.StatusReconnecting)},
		}},
	})
	if cmd := m.ChooseHostForNewWindow(stale); cmd != nil {
		t.Fatal("ASSERTION: a pick of a machine whose link is down was attempted")
	}
	if !notified(m, "build is unavailable") {
		t.Fatalf("the pick did not say build is unavailable: %+v", m.Notifications)
	}
}

// TestAHostPushKeepsTheClientEventListenerArmed.
//
// The daemon's host push arrives on the client event channel, which is read
// one event at a time. HostsChangedMsg did not arm the reader again, so the
// first link change was the last event the client heard. After that the
// machine picker read link states up to a minute old: a machine that came
// back was refused as unavailable, and one that went down was still offered.
//
// Negative control: returning refreshFederationCmd alone from the
// HostsChangedMsg case fails here.
func TestAHostPushKeepsTheClientEventListenerArmed(t *testing.T) {
	ownSocket(t) // the host refresh dials a daemon, and must find none
	m := &OS{Settings: config.Global, ClientEventChan: make(chan ClientEvent, 1)}
	_, cmd := m.Update(HostsChangedMsg{})
	if cmd == nil {
		t.Fatal("ASSERTION: HostsChangedMsg returned no command")
	}
	m.ClientEventChan <- ClientEvent{Type: "resize", Width: 1, Height: 2, ClientCount: 3}
	cmds := []tea.Cmd{cmd}
	for len(cmds) > 0 {
		c := cmds[0]
		cmds = cmds[1:]
		switch msg := c().(type) {
		case tea.BatchMsg:
			cmds = append(cmds, msg...)
		case SessionResizeMsg:
			return
		}
	}
	t.Fatal("ASSERTION: after a host push nothing reads the next client event")
}

// TestAnEmptySessionBringsItsGlobalMark.
//
// A new global session is empty, and switching to an empty session skipped
// the step that adopts the session's labels. The client kept the global mark
// of the session it left: the first pane of "global-2" was made on this
// machine without the picker, and a plain empty session asked for one.
//
// Negative control: dropping adoptSessionLabels from rebuildForSession's
// empty branch fails both halves.
func TestAnEmptySessionBringsItsGlobalMark(t *testing.T) {
	m := pickerOS(t)
	m.SessionGlobal = false
	m.rebuildForSession(&session.SessionState{Name: "global-2", Global: true}, m.Width, m.Height)
	if !m.SessionGlobal {
		t.Error("ASSERTION: an empty global session was not marked global")
	}
	m.rebuildForSession(&session.SessionState{Name: "plain"}, m.Width, m.Height)
	if m.SessionGlobal {
		t.Error("ASSERTION: an empty plain session kept the global mark of the one before")
	}
}
