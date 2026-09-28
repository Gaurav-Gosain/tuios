package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// #196 from the keyboard: Enter in the session switcher, attached to a session
// on build, on this machine's "home". The row's ID is the rail identity
// "\x00host/local:home", and Enter used to send that to build's daemon as a
// session name.

// switcherAwayOS is a client attached to "api" on build, with the switcher
// open on this machine's "home" and build's own "api".
func switcherAwayOS() *app.OS {
	o := &app.OS{
		Settings:     config.Global,
		AttachedHost: "build",
		SessionName:  "api",
	}
	o.ShowSessionSwitcher = true
	o.SessionSwitcherItems = []sessiontree.Node{
		{Kind: sessiontree.KindSession, ID: "api", Title: "api", IsCurrent: true},
		{Kind: sessiontree.KindSession, ID: "\x00host/local:home", Title: "home", Host: "local"},
	}
	return o
}

type switched struct{ host, name string }

func recordSwitchesOn(o *app.OS) *[]switched {
	var calls []switched
	o.SetSessionSwitchHookForTest(func(host, name string) error {
		calls = append(calls, switched{host, name})
		return nil
	})
	return &calls
}

// TestSwitcherEnterReachesThisMachineFromARemoteSession.
//
// Negative control: restoring SwitchToSession(selected.ID) in the enter case
// sends nothing through the hook and this fails.
func TestSwitcherEnterReachesThisMachineFromARemoteSession(t *testing.T) {
	o := switcherAwayOS()
	calls := recordSwitchesOn(o)
	o.SessionSwitcherSelected = 1

	handleSessionSwitcherInput(tea.KeyPressMsg{Code: tea.KeyEnter}, o)

	if len(*calls) != 1 || (*calls)[0] != (switched{"local", "home"}) {
		t.Fatalf("ASSERTION: enter sent the switch to %+v, want local/home", *calls)
	}
	if o.ShowSessionSwitcher {
		t.Errorf("the switcher stayed open after enter")
	}
}

// TestSwitcherKeepsRemoteRowsOutOfRenameAndDelete: rename and delete go to
// the daemon this client is connected to, which does not hold the session.
func TestSwitcherKeepsRemoteRowsOutOfRenameAndDelete(t *testing.T) {
	o := switcherAwayOS()
	o.SessionSwitcherSelected = 1

	handleSessionSwitcherInput(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, o)
	if o.SessionSwitcherConfirmDelete != "" {
		t.Errorf("ASSERTION: ctrl+d asked to delete another machine's session %q", o.SessionSwitcherConfirmDelete)
	}

	handleSessionSwitcherInput(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}, o)
	if o.Renaming() {
		t.Errorf("ASSERTION: ctrl+r began renaming another machine's session")
	}
}
