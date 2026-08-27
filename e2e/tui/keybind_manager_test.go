package tuie2e

import (
	"os"
	"path/filepath"
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
// The conflict is written into the config here. It used to come from the
// shipped defaults, which bound 1 to both select_window_1 (window_management)
// and snap_corner_1 (layout) and let the layout table win, so select_window_1
// had never fired in a default install. Reading that as a feature of the panel
// is how it survived: the test passed because the defaults were broken, and
// fixing them would have failed the test. The defaults now resolve every key to
// one action, guarded by TestDefaultConfigHasNoConflicts, and this case brings
// its own clash.
func TestKeybindManagerShowsARealConflictOnScreen(t *testing.T) {
	base := t.TempDir()
	cfgDir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	// Two actions in one scope on one key, in two different tables: the
	// cross-section case, which is the one config.toml gives no hint of.
	//
	// snap_left rather than snap_corner_1, which would be the historical pair.
	// migrateCornerSnapDigits takes a corner off a bare digit at load, so that
	// fixture is repaired before the overlay ever sees it, which is the
	// migration doing its job and is checked in
	// TestCornerSnapMigrationMovesTheStaleDigits. snap_left on "1" is a choice
	// the user made and nothing rewrites it.
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(
		"[keybindings.window_management]\nselect_window_1 = [\"1\"]\n"+
			"[keybindings.layout]\nsnap_left = [\"1\"]\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	term := startIn(t, base, startOpts{})
	waitBoot(t, term)
	openKeybindManager(t, term)

	// Tab twice: Bindings -> Conflicts.
	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("send tab: %v", err)
	}
	if err := term.WaitForText("snap_left", uiTimeout); err != nil {
		t.Fatalf("conflicts tab never named the winning action: %v\n%s", err, term.Snapshot())
	}

	screen := term.Screen().Text()
	t.Logf("keybind manager, Conflicts tab:\n%s", term.Snapshot())

	// The winner has to be named, or the panel is a list of keys rather than an
	// answer to "what does this key do".
	if !strings.Contains(screen, "runs snap_left") {
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
	// And the row has to be actionable. A panel that reports a problem and
	// offers nothing to press teaches the reader to distrust it.
	if !strings.Contains(screen, "ctrl+d") {
		t.Errorf("the conflicts panel offers no way to resolve the row\n%s", term.Snapshot())
	}
}

// TestStockConfigOpensNoConflicts is the maintainer's report, on screen: he
// opened this tab on a config he had never edited and found four.
//
// The unit invariant (TestDefaultConfigHasNoConflicts) checks the same thing
// against the registry. This checks it against the pixels, because the panel is
// where the claim is made and a report that disagreed with its own analysis
// would pass the unit test and still be wrong here.
func TestStockConfigOpensNoConflicts(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	openKeybindManager(t, term)

	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("send tab: %v", err)
	}
	if err := term.WaitForText("No conflicts", uiTimeout); err != nil {
		t.Fatalf("a config nobody edited reports conflicts: %v\n%s", err, term.Snapshot())
	}
	t.Logf("keybind manager, Conflicts tab on a stock config:\n%s", term.Snapshot())

	if strings.Contains(term.Screen().Text(), "dead") {
		t.Errorf("the stock config's conflicts panel names a dead binding\n%s", term.Snapshot())
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
	if !strings.Contains(strings.ToLower(screen), "program list") {
		t.Errorf("the guests tab must say its findings come from the program list, not detection\n%s", term.Snapshot())
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

// The observed tier has to be read from the pane rather than inferred. The
// harness runs /bin/sh in every window, so opening the overlay over a live pane
// must name it.
//
// This is the one tier that cannot be checked from the config alone: it goes
// through TIOCGPGRP and /proc, and its failure mode is a report that quietly
// says nothing about the pane rather than one that errors.
func TestKeybindManagerReadsTheLivePane(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	openKeybindManager(t, term)

	// The recorder's panel is where the observed block is drawn.
	for range 3 {
		if err := term.SendKeys(tuitest.Tab); err != nil {
			t.Fatalf("send tab: %v", err)
		}
	}
	if err := term.WaitForText("SEEN IN THIS PANE", uiTimeout); err != nil {
		t.Fatalf("the overlay never reported anything observed about the pane: %v\n%s", err, term.Snapshot())
	}
	t.Logf("keybind manager, observed block over a live pane:\n%s", term.Snapshot())

	if !strings.Contains(term.Screen().Text(), "is the foreground process") {
		t.Errorf("the pane runs a shell and the overlay must name it\n%s", term.Snapshot())
	}
}
