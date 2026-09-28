package app

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

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

// attachedTo puts m on a session on build, with build's own view of its
// hosts: it calls this machine "beta", and it has a link to "gpu" that is
// down. This machine's table knows "build" and "workstation" and nothing
// build knows.
func attachedTo(t *testing.T, m *OS) {
	t.Helper()
	m.AttachedHost = "build"
	m.HostPickerPurpose = HostPickerNewWindow
	m.applyAttachedHosts(AttachedHostsMsg{View: attachedHostView{
		Host: "build",
		Here: "beta",
		Hosts: []federation.HostReport{
			{Host: "beta", Status: federation.StatusUp, Sessions: 2},
			{Host: "gpu", Status: federation.StatusUnreachable},
		},
	}})
}

// TestFromAnotherMachineTheRowsAreThatMachinesHosts.
//
// A window is made by the daemon that holds the session, and that daemon
// reads a host name in its own table. The rows used this machine's names, so
// from build a pick of "beta" sent "beta" to build, which may call a
// different machine that, or this one. The rows are now build's hosts by
// build's names, and this machine is the row whose daemon is this one.
//
// Negative control: building the window rows from FederationHosts while
// attached lists "workstation" and sends "" for this machine, and fails.
func TestFromAnotherMachineTheRowsAreThatMachinesHosts(t *testing.T) {
	m := pickerOS(t)
	attachedTo(t, m)
	items := m.buildHostPickerItems()
	byLabel := map[string]HostPickerItem{}
	for _, it := range items {
		byLabel[it.Label] = it
	}
	if it, ok := byLabel["build"]; !ok || it.Name != "" || !it.Up {
		t.Errorf("ASSERTION: the session's own machine is not a plain window row: %+v", items)
	}
	if it, ok := byLabel["this machine"]; !ok || it.Name != "beta" || !it.Up {
		t.Errorf("ASSERTION: this machine is not listed by build's name for it: %+v", items)
	}
	if _, ok := byLabel["workstation"]; ok {
		t.Errorf("ASSERTION: a host only this machine knows is offered from build: %+v", items)
	}
	if it, ok := byLabel["gpu"]; !ok || it.Up {
		t.Errorf("ASSERTION: build's down link is not listed as down: %+v", items)
	}
	if cmd := m.ChooseHostForNewWindow(byLabel["this machine"]); cmd == nil {
		t.Error("ASSERTION: a pick of this machine from build asked build for nothing")
	}
}

// TestFromAnotherMachineThisMachineNeedsALinkBack.
//
// When the attached machine has no host that is this machine's daemon, no
// row may claim to be this machine.
func TestFromAnotherMachineThisMachineNeedsALinkBack(t *testing.T) {
	m := pickerOS(t)
	attachedTo(t, m)
	m.applyAttachedHosts(AttachedHostsMsg{View: attachedHostView{
		Host:  "build",
		Hosts: []federation.HostReport{{Host: "beta", Status: federation.StatusUp}},
	}})
	for _, it := range m.buildHostPickerItems() {
		if it.Label == "this machine" {
			t.Fatalf("ASSERTION: this machine is offered with no link back from build: %+v", it)
		}
	}
	// A session is still made here: the session picker keeps the row.
	m.HostPickerPurpose = HostPickerNewSession
	if items := m.buildHostPickerItems(); items[0].Label != "this machine" || !items[0].Up {
		t.Error("ASSERTION: the session picker refuses this machine from build")
	}
}

// TestFromAnotherMachineAPickChecksThatMachinesLink.
//
// The pick checks the link of the daemon that makes the window. This
// machine's own link to a machine with the same name says nothing about it.
//
// Negative control: checking hostIsUp instead refuses "beta" here, because
// this machine's table has no beta, and fails.
func TestFromAnotherMachineAPickChecksThatMachinesLink(t *testing.T) {
	m := pickerOS(t)
	attachedTo(t, m)
	if cmd := m.ChooseHostForNewWindow(HostPickerItem{Name: "beta", Label: "this machine", Up: true}); cmd == nil {
		t.Errorf("ASSERTION: a pick of a machine build reaches was refused: %+v", m.Notifications)
	}
	if cmd := m.ChooseHostForNewWindow(HostPickerItem{Name: "gpu", Label: "gpu", Up: true}); cmd != nil {
		t.Error("ASSERTION: a pick of a machine whose link from build is down was attempted")
	}
	if !notified(m, "gpu is unavailable") {
		t.Errorf("the pick did not say gpu is unavailable: %+v", m.Notifications)
	}
}

// TestFromAnotherMachineThePickerWaitsForTheList.
//
// Before build's list is in, the picker shows a row that says so, and fills
// in when the list lands. The loading row cannot be chosen.
func TestFromAnotherMachineThePickerWaitsForTheList(t *testing.T) {
	m := pickerOS(t)
	m.AttachedHost = "build"
	m.HostPickerPurpose = HostPickerNewWindow
	m.HostPickerItems = m.buildHostPickerItems()
	m.ShowHostPicker = true
	if !hostPickerWaiting(m.HostPickerItems) {
		t.Fatalf("no loading row before the list is in: %+v", m.HostPickerItems)
	}
	if cmd := m.ChooseHostForNewWindow(m.HostPickerItems[1]); cmd != nil {
		t.Error("ASSERTION: the loading row was chosen as a machine")
	}
	m.ShowHostPicker = true
	attachedTo(t, m)
	if hostPickerWaiting(m.HostPickerItems) {
		t.Errorf("ASSERTION: the picker was not filled in when the list landed: %+v", m.HostPickerItems)
	}
}

// TestThePickerCursorStartsOnAMachineThatCanBePicked.
//
// Enter on a fresh picker is the common pick. The cursor started on the first
// row whatever it was, and that row can be one that is down.
func TestThePickerCursorStartsOnAMachineThatCanBePicked(t *testing.T) {
	items := []HostPickerItem{{Label: "a"}, {Label: "b", Up: true}, {Label: "c", Up: true}}
	if got := FirstPickableHostRow(items); got != 1 {
		t.Errorf("FirstPickableHostRow = %d, want 1", got)
	}
	m := pickerOS(t)
	m.AttachedHost = "build"
	m.applyAttachedHosts(AttachedHostsMsg{View: attachedHostView{
		Host:  "build",
		Hosts: []federation.HostReport{{Host: "gpu", Status: federation.StatusUnreachable}},
	}})
	m.SessionGlobal = true
	m.OpenHostPicker()
	if !m.ShowHostPicker {
		t.Fatal("the picker did not open")
	}
	if it := m.HostPickerItems[m.HostPickerSelected]; !it.Up {
		t.Errorf("ASSERTION: the cursor starts on a row that cannot be picked: %+v", it)
	}
}

// TestMakingASessionHereDoesNotBlockTheUI.
//
// From a session on another machine, a new session on this machine is a
// connect, a create and an attach. They ran on the Update goroutine, so a
// daemon slow to answer froze input and drawing for the whole wait. Here the
// daemon's socket accepts and never answers.
//
// Negative control: calling open inline in makeSessionHere blocks until the
// deadline and fails.
func TestMakingASessionHereDoesNotBlockTheUI(t *testing.T) {
	ownSocket(t)
	path, err := session.GetSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			_ = c.Close()
		}
	})

	m := pickerOS(t)
	m.AttachedHost = "build"
	done := make(chan struct{})
	go func() {
		m.SidebarNewSession()
		m.SidebarNewGlobalSession()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ASSERTION: making a session on this machine blocked the UI goroutine on a silent daemon")
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
