package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// The machine picker is the way to put a window on another machine from inside
// the UI. Until it existed the only way was the command line, which meant the
// feature was invisible to anyone using tuios rather than scripting it.

func pickerOS(t *testing.T) *OS {
	t.Helper()
	m := sidebarTestOS(t, 120, 40, "left")
	m.SessionName = "work"
	m.IsDaemonSession = true
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
	m := pickerOS(t)
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

// TestThePickerDoesNotOpenWithNoOtherMachines. A dialog asking a question with
// one answer is not a choice, and the answer is the one a plain new window
// already gives.
func TestThePickerDoesNotOpenWithNoOtherMachines(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.IsDaemonSession = true
	m.applyFederationSnapshot(FederationHostsMsg{Snapshot: FederationSnapshot{Hosts: []FederationHost{
		{Name: federation.LocalHostName, Status: string(federation.StatusUp)},
	}}})

	m.OpenHostPicker()
	if m.ShowHostPicker {
		t.Error("the picker opened with only this machine to offer")
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
