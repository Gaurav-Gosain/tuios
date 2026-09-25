package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
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

// TestAMachineThatIsDownIsOfferedAndRefused.
//
// Listed, because hiding it leaves someone wondering whether they imagined
// configuring it, which is the argument the rail already makes for keeping a
// host's heading when its link is down. Refused, because the attempt is a link
// dial with a twenty second budget on the end of it and the answer is known.
//
// Negative control: dropping the Up check in ChooseHostForNewWindow returns a
// command here instead of nil.
func TestAMachineThatIsDownIsOfferedAndRefused(t *testing.T) {
	m := pickerOS(t)
	items := m.buildHostPickerItems()

	var down HostPickerItem
	for _, it := range items {
		if it.Name == "workstation" {
			down = it
		}
	}
	if down.Name == "" {
		t.Fatal("a machine that is down is not offered at all")
	}
	if down.Up {
		t.Fatal("ASSERTION: the fixture's machine is up, so this proves nothing")
	}

	if cmd := m.ChooseHostForNewWindow(down); cmd != nil {
		t.Error("a window was opened on a machine that is not answering")
	}
	if m.ShowHostPicker {
		t.Error("the picker stayed open after a choice")
	}
}

// TestOnlyTheGlobalSessionAsksWhichMachine.
//
// A local session is the machine it is on: a new pane in it is a pane there,
// and asking every time would be a question with one sensible answer. The
// global session is the one place mixing machines is the point, so it is the
// one place the question is put.
//
// Negative control: gating on the machine count alone, which is what this did
// first, makes the local session ask too and fails here.
func TestOnlyTheGlobalSessionAsksWhichMachine(t *testing.T) {
	m := pickerOS(t)

	m.SessionName = "work"
	if m.newWindowShouldPickHost() {
		t.Error("an ordinary session asked which machine a new pane goes on")
	}

	m.SessionName = GlobalSessionName
	if !m.newWindowShouldPickHost() {
		t.Error("the global session did not ask which machine a new pane goes on")
	}
}

// TestTheGlobalSessionDoesNotAskWithNowhereToGo. A picker offering one row is
// a question with one answer, even in the session built for choosing.
func TestTheGlobalSessionDoesNotAskWithNowhereToGo(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.IsDaemonSession = true
	m.SessionName = GlobalSessionName
	m.applyFederationSnapshot(FederationHostsMsg{Snapshot: FederationSnapshot{Hosts: []FederationHost{
		{Name: federation.LocalHostName, Status: string(federation.StatusUp)},
	}}})

	if m.newWindowShouldPickHost() {
		t.Error("the global session asked which machine with only this one reachable")
	}
}

// railGlobalGroup is the rail's global group: the header's index among the
// rows, and the rows listed under it.
func railGlobalGroup(t *testing.T, m *OS, here []sessiontree.Node) (int, []sessiontree.Node) {
	t.Helper()
	rows := m.sidebarMachineRows(here, m.hostGroupNodes())
	for i, n := range rows {
		if n.Kind != sessiontree.KindHost || !n.Global {
			continue
		}
		var under []sessiontree.Node
		for _, r := range rows[i+1:] {
			if r.Kind == sessiontree.KindHost {
				break
			}
			under = append(under, r)
		}
		return i, under
	}
	return -1, nil
}

// TestAGlobalSessionIsListedInTheGlobalGroupAndNotUnderItsMachine.
//
// Negative control: without the strip in sidebarMachineRows the session stays
// under this machine and the global group is empty.
func TestAGlobalSessionIsListedInTheGlobalGroupAndNotUnderItsMachine(t *testing.T) {
	m := pickerOS(t)
	here := []sessiontree.Node{
		{Kind: sessiontree.KindSession, ID: "work", Title: "work"},
		{Kind: sessiontree.KindSession, ID: "everywhere", Title: "everywhere", Global: true},
	}

	rows := m.sidebarMachineRows(here, m.hostGroupNodes())
	seen := 0
	for _, n := range rows {
		if n.Kind == sessiontree.KindSession && n.ID == "everywhere" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("the global session is drawn %d times, want once", seen)
	}

	_, under := railGlobalGroup(t, m, here)
	if len(under) != 1 || under[0].ID != "everywhere" {
		t.Errorf("the global group holds %+v, want the global session", under)
	}
}

// TestASessionNamedGlobalIsStillGlobal. The mark is set when the session is
// created, so the sessions created before the mark existed have only their
// name to say what they are.
func TestASessionNamedGlobalIsStillGlobal(t *testing.T) {
	m := pickerOS(t)
	here := []sessiontree.Node{
		{Kind: sessiontree.KindSession, ID: "work", Title: "work"},
		{Kind: sessiontree.KindSession, ID: GlobalSessionName, Title: GlobalSessionName},
	}

	_, under := railGlobalGroup(t, m, here)
	if len(under) != 1 || under[0].ID != GlobalSessionName {
		t.Errorf("the group holds %+v, want the session named global", under)
	}
}

// TestTurningTheGlobalSessionOffRemovesTheGroup, when there is no global
// session to show. A session that exists is still listed: hiding a session
// somebody is using is worse than showing a group they turned off.
func TestTurningTheGlobalSessionOffRemovesTheGroup(t *testing.T) {
	m := pickerOS(t)
	m.Settings.GlobalSession = false
	if m.GlobalSessionOffered() {
		t.Error("the group is offered with the setting off")
	}
	here := []sessiontree.Node{{Kind: sessiontree.KindSession, ID: "work", Title: "work"}}
	if at, _ := railGlobalGroup(t, m, here); at != -1 {
		t.Error("the group was drawn with the setting off and nothing in it")
	}
}

// TestEveryWayOfMakingAWindowInAGlobalSessionAsksWhichMachine.
//
// The picker was wired into the key, the rail's "+" and the palette, and the
// splits were not: they called AddWindow directly, so ctrl+b | and ctrl+b -
// made a local pane in the one session whose point is that the machine is
// chosen. A question asked by three routes out of five is not a rule anybody
// can rely on.
//
// Negative control: putting AddWindow back in the daemon branch of either
// split leaves ShowHostPicker false and this fails.
func TestEveryWayOfMakingAWindowInAGlobalSessionAsksWhichMachine(t *testing.T) {
	for _, tc := range []struct {
		name  string
		split func(*OS)
	}{
		{"split down", (*OS).SplitFocusedHorizontal},
		{"split right", (*OS).SplitFocusedVertical},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := pickerOS(t)
			m.SessionGlobal = true
			m.AutoTiling = true
			m.Windows = []*terminal.Window{{ID: "w1", Width: 40, Height: 20, Workspace: 1}}
			m.CurrentWorkspace = 1
			m.FocusedWindow = 0

			tc.split(m)

			if !m.ShowHostPicker {
				t.Error("the split did not ask which machine the pane runs on")
			}
			if m.pendingSplitTarget != "w1" {
				t.Errorf("the split recorded %q as the pane to split against, want w1", m.pendingSplitTarget)
			}
		})
	}
}

// TestCancellingThePickerForgetsTheSplit.
//
// A split records the direction and the pane before it asks for the window,
// because the window is made by the daemon and arrives later. If the question
// is cancelled that record has to go: otherwise the next window made for any
// reason lands split against a pane the user has since forgotten about.
//
// Negative control: closing the picker with a bare ShowHostPicker = false
// leaves the target set and this fails.
func TestCancellingThePickerForgetsTheSplit(t *testing.T) {
	m := pickerOS(t)
	m.SessionGlobal = true
	m.AutoTiling = true
	m.Windows = []*terminal.Window{{ID: "w1", Width: 40, Height: 20, Workspace: 1}}
	m.CurrentWorkspace = 1
	m.FocusedWindow = 0

	m.SplitFocusedHorizontal()
	if m.pendingSplitTarget == "" {
		t.Fatal("ASSERTION: the split recorded nothing, so there is nothing to forget")
	}

	m.CloseHostPicker()

	if m.ShowHostPicker {
		t.Error("the picker is still open")
	}
	if m.pendingSplitTarget != "" {
		t.Errorf("a cancelled split still points at %q", m.pendingSplitTarget)
	}
	if m.pendingSplitDir != layout.PreselectionNone {
		t.Error("a cancelled split still forces a direction")
	}
}

// TestMakingASessionAsksWhichMachineWhenThereIsAChoice.
//
// Creating a session had one control anywhere on screen: a one-cell "+" on a
// rail heading, only present while the rail is open, carrying no label, and
// only ever making a session on this machine. With more than one machine
// configured, "new session" is a question, and it is now asked by the dock's
// control, the palette entry and the rail's "+" alike.
//
// Negative control: routing the rail's "+" straight to SidebarNewSession
// leaves ShowHostPicker false and this fails.
func TestMakingASessionAsksWhichMachineWhenThereIsAChoice(t *testing.T) {
	m := pickerOS(t)

	m.SidebarNewSessionHere()

	if !m.ShowHostPicker {
		t.Fatal("making a session did not ask which machine with two reachable")
	}
	if m.HostPickerPurpose != HostPickerNewSession {
		t.Error("the picker opened for the wrong purpose")
	}
}

// TestMakingASessionOnOneMachineAsksNothing. A picker with a single row is a
// dialog that asks a question with one answer.
//
// The gate is checked rather than the whole call, because on one machine the
// call goes on to create the session and this fixture holds a client with no
// connection behind it.
func TestMakingASessionOnOneMachineAsksNothing(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.IsDaemonSession = true
	m.DaemonClient = session.NewTUIClient()
	m.applyFederationSnapshot(FederationHostsMsg{Snapshot: FederationSnapshot{Hosts: []FederationHost{
		{Name: federation.LocalHostName, Status: string(federation.StatusUp)},
	}}})

	if m.newSessionShouldPickHost() {
		t.Error("a machine that can reach nowhere else would be asked which machine")
	}

	// And with a second one up, it is a question.
	m.applyFederationSnapshot(FederationHostsMsg{Snapshot: FederationSnapshot{Hosts: []FederationHost{
		{Name: federation.LocalHostName, Status: string(federation.StatusUp)},
		{Name: "build", Status: string(federation.StatusUp)},
	}}})
	if !m.newSessionShouldPickHost() {
		t.Error("two reachable machines and making a session still asked nothing")
	}
}

// TestTheSessionPickerOffersAGlobalSession.
//
// "New session on" asks which machine, and a global session is an answer to
// that question: it is the session whose panes are not on any one of them. It
// belongs in the same list rather than behind a second control somewhere else.
//
// Negative control: dropping the global row from buildHostPickerItems leaves
// only the machines and this fails.
func TestTheSessionPickerOffersAGlobalSession(t *testing.T) {
	m := pickerOS(t)
	m.HostPickerPurpose = HostPickerNewSession

	items := m.buildHostPickerItems()
	var global *HostPickerItem
	for i := range items {
		if items[i].Global {
			global = &items[i]
		}
	}
	if global == nil {
		t.Fatal("the session picker does not offer a global session")
	}
	if global.Label != GlobalSessionName {
		t.Errorf("the global row reads %q", global.Label)
	}
	// First, because a session holding panes from several machines is not a
	// session on any of the machines listed under it.
	if items[1].Global != true {
		t.Error("the global row is not the first thing after this machine")
	}
}

// TestTheWindowPickerDoesNotOfferAGlobalSession. A window is made in the
// session you are already in, and "global" is not a machine to run a process
// on, so the row would be an answer to a question nobody asked.
func TestTheWindowPickerDoesNotOfferAGlobalSession(t *testing.T) {
	m := pickerOS(t)
	m.HostPickerPurpose = HostPickerNewWindow

	for _, it := range m.buildHostPickerItems() {
		if it.Global {
			t.Error("the window picker offered a global session as a machine")
		}
	}
}

// TestTheGlobalRowIsNotOfferedWhenTheGroupIsNot. The row and the rail's group
// answer to one setting, so turning global sessions off takes both.
func TestTheGlobalRowIsNotOfferedWhenTheGroupIsNot(t *testing.T) {
	m := pickerOS(t)
	m.HostPickerPurpose = HostPickerNewSession
	m.Settings.GlobalSession = false

	for _, it := range m.buildHostPickerItems() {
		if it.Global {
			t.Error("the global row was offered with global sessions turned off")
		}
	}
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
