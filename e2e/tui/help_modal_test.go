package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// helpTitle is the header the keybindings overlay renders. Asserting on it
// proves the overlay is on screen rather than that a state flag was flipped.
const helpTitle = "Keybindings"

// openHelp opens the help overlay from window-management mode and waits for it.
func openHelp(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys("?"); err != nil {
		t.Fatalf("send '?': %v", err)
	}
	if err := term.WaitForText(helpTitle, uiTimeout); err != nil {
		t.Fatalf("help overlay did not open: %v\n%s", err, term.Snapshot())
	}
}

// TestHelpOverlayIsModalInWindowMode pins the help overlay swallowing keys it
// does not itself handle, in window-management mode.
//
// The overlay handled esc/q/?/arrows/'/' and search typing and then fell
// through to the normal window-manager dispatch for everything else, while
// covering most of the screen. Pressing n with help open created and focused a
// window behind the panel, x closed one, t toggled tiling: the state changed
// under an overlay that shows none of it, and the user's next keystroke went
// somewhere they had no way to predict. Terminal mode already ignored unhandled
// keys while help was up, so the same key did two different things depending on
// a mode the overlay hides.
//
// Each subtest asserts on the dock's window count, which is the state these
// bindings change and the one thing still visible beside the panel, and on the
// overlay still being up afterwards.
//
// Verified failing against a binary built from the parent commit: with two
// windows and help open, n took the dock from 1:2 to 1:3.
func TestHelpOverlayIsModalInWindowMode(t *testing.T) {
	// Both keys are asserted through the dock's window count, which is the one
	// piece of state the overlay does not cover. Bindings whose effect the panel
	// hides entirely (t, i, ',') are pinned in
	// internal/input.TestHelpOverlaySwallowsUnhandledKeys instead, which can see
	// the whole model.
	cases := []struct {
		name string
		key  string
	}{
		{"new-window", "n"},
		{"close-window", "x"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term, _ := start(t, startOpts{})
			waitBoot(t, term)
			newWindow(t, term)
			newWindow(t, term)

			before := settledWindowCount(t, term)
			if before != 2 {
				t.Fatalf("expected 2 windows before opening help, dock says %d\n%s", before, term.Snapshot())
			}

			openHelp(t, term)

			if err := term.SendKeys(tc.key); err != nil {
				t.Fatalf("send %q: %v", tc.key, err)
			}

			// There is no event to wait for when the fix works, so wait for the
			// failure instead and require it not to arrive: if the key reaches
			// the window manager the count changes within the UI timeout.
			err := term.WaitFor(func(s tuitest.Screen) bool {
				n := countWindows(s)
				return n >= 0 && n != before
			}, uiTimeout)
			if err == nil {
				t.Fatalf("%q reached the window manager with help open: window count %d -> %d\n%s",
					tc.key, before, countWindows(term.Screen()), term.Snapshot())
			}

			if !strings.Contains(term.Screen().Text(), helpTitle) {
				t.Fatalf("help overlay left the screen after %q\n%s", tc.key, term.Snapshot())
			}

			// The overlay must still close on a key it does handle, so this is
			// modality rather than a wedged input path.
			if err := term.SendKeys(tuitest.Esc); err != nil {
				t.Fatalf("send esc: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return !strings.Contains(s.Text(), helpTitle)
			}, uiTimeout); err != nil {
				t.Fatalf("help overlay did not close on esc: %v\n%s", err, term.Snapshot())
			}
			alive(t, term, "after closing help")
		})
	}
}
