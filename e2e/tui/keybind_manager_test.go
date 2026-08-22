package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// keybindTitle is the keybind manager's panel title. Asserting on it proves the
// overlay painted rather than that a state flag flipped.
const keybindTitle = "Keybinds"

// openKeybindManager opens the overlay through the leader chord and waits for
// it to paint.
func openKeybindManager(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), "k"); err != nil {
		t.Fatalf("send leader chord: %v", err)
	}
	if err := term.WaitForText(keybindTitle, uiTimeout); err != nil {
		t.Fatalf("keybind manager did not open: %v\n%s", err, term.Snapshot())
	}
}

// The overlay's whole claim is that it shows a real conflict, so the test is
// that a real conflict is legible on the screen, not that a panel appeared.
//
// The shipped defaults bind 1 to both select_window_1 (window_management) and
// snap_corner_1 (layout). layout is copied over window_management, so the snap
// wins and select_window_1 has never fired. Nothing in config.toml says so.
func TestKeybindManagerShowsARealConflictOnScreen(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	openKeybindManager(t, term)

	// Tab twice: Bindings -> Conflicts.
	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("send tab: %v", err)
	}
	if err := term.WaitForText("snap_corner_1", uiTimeout); err != nil {
		t.Fatalf("conflicts tab never named the winning action: %v\n%s", err, term.Snapshot())
	}

	screen := term.Screen().Text()
	t.Logf("keybind manager, Conflicts tab:\n%s", term.Snapshot())

	// The winner has to be named, or the panel is a list of keys rather than an
	// answer to "what does this key do".
	if !strings.Contains(screen, "runs snap_corner_1") {
		t.Errorf("the conflict row must name the action that actually runs\n%s", term.Snapshot())
	}
	// And the loser has to be named in the detail box, or there is no way to
	// tell which of the two bindings is the dead one.
	if !strings.Contains(screen, "select_window_1") {
		t.Errorf("the detail box must name the binding that never fires\n%s", term.Snapshot())
	}
	// The conflict must be legible without colour: "dead" is the word that
	// carries it.
	if !strings.Contains(screen, "dead") {
		t.Errorf("a conflict must say so in words, not only in colour\n%s", term.Snapshot())
	}
}

// The guests tab's headline case: tuios's leader is tmux's prefix, and tuios
// takes it before the pane sees it.
func TestKeybindManagerShowsTheGuestClash(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	openKeybindManager(t, term)

	for range 2 {
		if err := term.SendKeys(tuitest.Tab); err != nil {
			t.Fatalf("send tab: %v", err)
		}
	}
	if err := term.WaitForText("tmux", uiTimeout); err != nil {
		t.Fatalf("guests tab never named tmux: %v\n%s", err, term.Snapshot())
	}

	screen := term.Screen().Text()
	t.Logf("keybind manager, Guests tab:\n%s", term.Snapshot())

	// The evidence tier has to be on screen. A curated list presented as
	// detection is the one failure mode this surface must not have.
	if !strings.Contains(strings.ToLower(screen), "curated") {
		t.Errorf("the guests tab must say its findings are curated, not detected\n%s", term.Snapshot())
	}
}

// The recorder swallows the key it is armed for, including keys that would
// otherwise close the overlay. Esc is the one that matters: if Esc still closed
// the panel, Esc could never be recorded.
func TestKeybindRecorderCapturesRatherThanRuns(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	openKeybindManager(t, term)

	arm := func() {
		t.Helper()
		if err := term.SendKeys(tuitest.Ctrl('r')); err != nil {
			t.Fatalf("send ctrl+r: %v", err)
		}
		if err := term.WaitForText("Press any key", uiTimeout); err != nil {
			t.Fatalf("recorder never armed: %v\n%s", err, term.Snapshot())
		}
	}

	// A key that does not appear in the overlay's own chrome, so matching it
	// cannot be a false positive on the footer.
	arm()
	if err := term.SendKeys(tuitest.Ctrl('g')); err != nil {
		t.Fatalf("send ctrl+g: %v", err)
	}
	if err := term.WaitForText("ctrl+g", uiTimeout); err != nil {
		t.Fatalf("ctrl+g was not captured: %v\n%s", err, term.Snapshot())
	}
	t.Logf("keybind manager, Record tab after capturing ctrl+g:\n%s", term.Snapshot())

	// Esc while armed is recorded rather than obeyed. Asserted on a verdict
	// appearing and on the panel still being up: the footer contains the word
	// "esc", so text matching alone would pass either way.
	arm()
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "bound in") || strings.Contains(s.Text(), "free:")
	}, uiTimeout); err != nil {
		t.Fatalf("esc produced no verdict, so it was not captured: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), keybindTitle) {
		t.Fatalf("esc closed the overlay instead of being recorded\n%s", term.Snapshot())
	}
	t.Logf("keybind manager, Record tab after capturing esc:\n%s", term.Snapshot())

	// And the press after a capture is a command again, so there is always a way
	// out. q rather than a second Esc: two escapes in quick succession are
	// parsed on the wire as one alt+esc, which is a property of the terminal and
	// not of the recorder.
	if err := term.SendKeys("q"); err != nil {
		t.Fatalf("send q: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), keybindTitle)
	}, uiTimeout); err != nil {
		t.Fatalf("overlay stayed open after the recorder disarmed: %v\n%s", err, term.Snapshot())
	}
}
