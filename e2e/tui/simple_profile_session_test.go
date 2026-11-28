package tuie2e

import (
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSimpleProfileDockButtonsInASession clicks the Simple dock's buttons in
// a named daemon session at 160x30, as "tuios new NAME" starts it. Each click
// comes as soon as the button is on screen, the moment a person clicks it.
// The daemon's notices ("Window created") come up in the dock as the panes
// come and go, and the buttons stay beside them.
//
// Negative control: drop the dockButtonsBeside block from renderDockString.
// A message then takes the buttons' place, the Split click lands on
// "Window created", and the wait for the split times out. On main the attach's
// "Session size" message does the same to the New click.
func TestSimpleProfileDockButtonsInASession(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "profile = \"simple\"\n")
	term := startIn(t, base, startOpts{cols: 160, rows: 30, args: []string{"new", "simpledock"}})
	killDaemon(t, base)
	waitBoot(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(tuitest.Screen(s), "Menu", "New", "Split", "Close", "Settings")
	}, uiTimeout); err != nil {
		t.Fatalf("the Simple dock never showed its buttons: %v\n%s", err, term.Snapshot())
	}

	clickDockWord(t, term, "New")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return paneCount(s) == 1 && !screenHas(tuitest.Screen(s), "Terminal UI Operating System")
	}, uiTimeout); err != nil {
		t.Fatalf("the New button opened no pane: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(tuitest.Screen(s), "Window created", "Split", "Settings")
	}, uiTimeout); err == nil {
		simpleShot(t, term, "simple-session-message-beside-buttons")
	}
	clickDockWord(t, term, "Split")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return paneCount(s) == 2 }, uiTimeout); err != nil {
		t.Fatalf("the Split button did not split the pane: %v\n%s", err, term.Snapshot())
	}
	simpleShot(t, term, "simple-session-split")
	clickDockWord(t, term, "Close")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return paneCount(s) == 1 }, uiTimeout); err != nil {
		t.Fatalf("the Close button did not close the pane: %v\n%s", err, term.Snapshot())
	}
	wl, err := daemonWindows(base, "simpledock")
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	if wl.Total != 1 {
		t.Fatalf("the daemon holds %d windows after New, Split and Close, want 1", wl.Total)
	}

	clickDockWord(t, term, "Settings")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(tuitest.Screen(s), "Appearance")
	}, uiTimeout); err != nil {
		t.Fatalf("the Settings button did not open settings: %v\n%s", err, term.Snapshot())
	}
	simpleShot(t, term, "simple-session-settings")
}
