package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
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

// TestTheGlobalSessionIsOfferedBeforeItExists. It is the session nobody has
// created yet that most needs a row, and switching to one that is not there
// creates it, so the row needs no second path behind it.
func TestTheGlobalSessionIsOfferedBeforeItExists(t *testing.T) {
	m := pickerOS(t)

	got := m.withGlobalSession(nil)
	if len(got) != 1 || got[0].Name != GlobalSessionName {
		t.Fatalf("the rail does not offer a global session row: %+v", got)
	}

	// And it is not offered twice once the daemon lists it.
	existing := []sessiontree.SessionInput{{Name: GlobalSessionName, WindowCount: 2}}
	again := m.withGlobalSession(existing)
	if len(again) != 1 {
		t.Errorf("the global session is listed %d times", len(again))
	}
	if again[0].WindowCount != 2 {
		t.Error("the real session was replaced by the empty offer")
	}
}

// TestTurningTheGlobalSessionOffRemovesTheRow.
func TestTurningTheGlobalSessionOffRemovesTheRow(t *testing.T) {
	m := pickerOS(t)
	m.Settings.GlobalSession = false
	if m.GlobalSessionOffered() {
		t.Error("the row is offered with the setting off")
	}
	if got := m.withGlobalSession(nil); len(got) != 0 {
		t.Errorf("a row was added with the setting off: %+v", got)
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

// TestTheGlobalRowDoesNotMoveTheMachineHeadings.
//
// The rail's machine headings are ordered from the host table so that
// switching sessions does not move them. A row that appeared under this
// machine only while the client happened to be attached to it would undo
// that: every switch away would take a row out of the group above the
// headings and step all of them up.
//
// So the offer does not depend on where the client is attached. It is under
// this machine either way, whether this machine is the attached group or a
// host group seen from somewhere else.
//
// Negative control: gating GlobalSessionOffered on AttachedHost == "" makes
// the two counts differ here, which is what moved the headings in the
// end-to-end rail test.
func TestTheGlobalRowDoesNotMoveTheMachineHeadings(t *testing.T) {
	m := pickerOS(t)
	local := FederationHost{Name: federation.LocalHostName, Status: string(federation.StatusUp)}

	// The two states use different paths, because hostGroupNodes leaves out
	// whichever machine is attached: attached here, this machine's sessions
	// come from live state through withGlobalSession; attached away, they come
	// from the host listing through hostSessionsWithGlobal. The row has to be
	// in whichever one is carrying this machine.
	m.AttachedHost = ""
	here := len(m.withGlobalSession(nil))

	m.AttachedHost = "build"
	away := len(m.hostSessionsWithGlobal(local))

	if here != away {
		t.Errorf("this machine's group gains %d rows when attached here and %d when away", here, away)
	}
	if here == 0 {
		t.Fatal("ASSERTION: no row is offered either way, so this proves nothing")
	}
}

// TestTheGlobalRowIsOnlyUnderThisMachine. It is this daemon's session, and a
// row under another machine's heading would propose creating one over there.
func TestTheGlobalRowIsOnlyUnderThisMachine(t *testing.T) {
	m := pickerOS(t)
	other := FederationHost{Name: "build", Status: string(federation.StatusUp)}
	if got := m.hostSessionsWithGlobal(other); len(got) != 0 {
		t.Errorf("another machine's group was offered a global session: %+v", got)
	}
}
