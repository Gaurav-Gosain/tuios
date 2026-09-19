package app

import (
	"strings"
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

// TestThisMachineIsTheFirstChoice. The list is a list of places to run a
// process and this machine is one of them, so a picker that offered only the
// remote ones would make the common answer the one you cannot pick.
func TestThisMachineIsTheFirstChoice(t *testing.T) {
	items := pickerOS(t).buildHostPickerItems()
	if len(items) == 0 {
		t.Fatal("the picker offers nothing")
	}
	if items[0].Name != "" {
		t.Errorf("the first choice is %q, want this machine", items[0].Name)
	}
	if !items[0].Up {
		t.Error("this machine is listed as unavailable")
	}
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

// TestChoosingThisMachineDoesNotGoOverALink. The local answer is the one that
// must stay instant: it is the common case and there is nothing to dial.
func TestChoosingThisMachineDoesNotGoOverALink(t *testing.T) {
	// Without a daemon client, so the pane is made the standalone way and the
	// question under test, whether anything was sent, is the only one asked.
	m := sidebarTestOS(t, 120, 40, "left")
	if cmd := m.ChooseHostForNewWindow(HostPickerItem{Name: "", Label: "this machine", Up: true}); cmd != nil {
		t.Error("choosing this machine returned a command, so it went out over the network")
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

// TestTheQueryNarrowsTheList, by the same substring match the other lists use.
func TestTheQueryNarrowsTheList(t *testing.T) {
	items := pickerOS(t).buildHostPickerItems()

	got := FilterHostPickerItems(items, "work")
	if len(got) != 1 || got[0].Name != "workstation" {
		t.Errorf("filtering for 'work' gave %d rows, want just workstation", len(got))
	}
	if len(FilterHostPickerItems(items, "")) != len(items) {
		t.Error("an empty query dropped rows")
	}
	if len(FilterHostPickerItems(items, "nothing-like-this")) != 0 {
		t.Error("a query matching nothing still returned rows")
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

// TestTheRailOffersTheGlobalSessionOnceASecondMachineIsThere, and not before:
// on a machine that can reach nowhere else it would be a session for holding
// panes from several machines on a machine that knows of none.
func TestTheRailOffersTheGlobalSessionOnceASecondMachineIsThere(t *testing.T) {
	m := pickerOS(t)
	if !m.GlobalSessionOffered() {
		t.Error("the rail does not offer the global session with another machine up")
	}

	alone := sidebarTestOS(t, 120, 40, "left")
	alone.IsDaemonSession = true
	alone.applyFederationSnapshot(FederationHostsMsg{Snapshot: FederationSnapshot{Hosts: []FederationHost{
		{Name: federation.LocalHostName, Status: string(federation.StatusUp)},
	}}})
	if alone.GlobalSessionOffered() {
		t.Error("the rail offers the global session on a machine that can reach nowhere else")
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

// TestTheGlobalGroupIsOfferedBeforeThereIsAnythingInIt. It is the group with
// nothing in it that most needs a header: its "+" is how the first global
// session gets made.
func TestTheGlobalGroupIsOfferedBeforeThereIsAnythingInIt(t *testing.T) {
	m := pickerOS(t)
	here := []sessiontree.Node{{Kind: sessiontree.KindSession, ID: "work", Title: "work"}}

	at, under := railGlobalGroup(t, m, here)
	if at != 0 {
		t.Fatalf("the global group is at row %d, want the top of the rail", at)
	}
	if len(under) != 0 {
		t.Errorf("the empty global group lists %d rows", len(under))
	}
}

// TestTheGlobalGroupIsAboveThisMachine.
//
// It is not a machine and it is not under one. A global session holds panes
// from several machines, so filing it under the one whose daemon happens to
// hold it says it belongs to that machine, which is the one thing it does not.
func TestTheGlobalGroupIsAboveThisMachine(t *testing.T) {
	m := pickerOS(t)
	here := []sessiontree.Node{{Kind: sessiontree.KindSession, ID: "work", Title: "work"}}

	rows := m.sidebarMachineRows(here, m.hostGroupNodes())
	var headers []string
	for _, n := range rows {
		if n.Kind == sessiontree.KindHost {
			headers = append(headers, n.Host)
		}
	}
	if len(headers) < 2 {
		t.Fatalf("ASSERTION: the rail drew %d machine headers, so there is no order to check", len(headers))
	}
	if headers[0] != GlobalSessionName {
		t.Errorf("the rail reads %v, want the global group first", headers)
	}
	if headers[1] != federation.LocalHostName {
		t.Errorf("the rail reads %v, want this machine under the global group", headers)
	}
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

// TestTheGlobalGroupHoldsMoreThanOneSession. There is nothing special about
// the first one: a person can keep as many as they have things to do.
func TestTheGlobalGroupHoldsMoreThanOneSession(t *testing.T) {
	m := pickerOS(t)
	here := []sessiontree.Node{
		{Kind: sessiontree.KindSession, ID: "work", Title: "work"},
		{Kind: sessiontree.KindSession, ID: "deploy", Title: "deploy", Global: true},
		{Kind: sessiontree.KindSession, ID: "debug", Title: "debug", Global: true},
	}

	_, under := railGlobalGroup(t, m, here)
	if len(under) != 2 {
		t.Errorf("the global group holds %d sessions, want both: %+v", len(under), under)
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

// TestAFailureIsReported. The window arriving is how success is seen, so only
// the failure needs saying, and it has to say which machine.
func TestAFailureIsReported(t *testing.T) {
	m := pickerOS(t)
	m.ApplyNewWindowOnHost(NewWindowOnHostMsg{Host: "build", Err: errFake{}})

	found := false
	for _, n := range m.Notifications {
		if strings.Contains(n.Message, "build") {
			found = true
		}
	}
	if !found {
		t.Error("a failed open said nothing, or did not name the machine")
	}
}

type errFake struct{}

func (errFake) Error() string { return "the link went away" }

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

// TestASplitOutsideAGlobalSessionAsksNothing. The question has one sensible
// answer in an ordinary session, and a picker that appeared there would put a
// dialog in front of a key people press all day.
//
// The split is not run here, because outside a global session it goes on to
// make the window and this fixture holds a client with no connection. What is
// checked is the gate the split now goes through, which is the thing that
// decides whether the question is asked at all.
func TestASplitOutsideAGlobalSessionAsksNothing(t *testing.T) {
	m := pickerOS(t)
	m.SessionGlobal = false
	m.SessionName = "work"

	if m.newWindowShouldPickHost() {
		t.Error("an ordinary session would be asked which machine a new pane runs on")
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
