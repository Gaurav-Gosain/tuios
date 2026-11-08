package tuie2e

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Kitty keyboard reports a modifier pressed on its own as a key of its own,
// with the code of that modifier key: CSI 57441 u is left shift, 57442 left
// ctrl, 57444 left super. The modifier field adds the modifier itself, so a
// lone left ctrl press is CSI 57442 ; 5 u. A chord with super is the key's
// code with modifier 9 (1 + super 8): CSI 107 ; 9 u is super+k.
const (
	kittyLeftShift = "\x1b[57441;2u"
	kittyLeftCtrl  = "\x1b[57442;5u"
	kittyLeftSuper = "\x1b[57444;9u"
	kittySuperK    = "\x1b[107;9u"
)

// TestShowKeysSkipsModifierPresses turns on the keycast and sends what a kitty
// terminal sends when a person holds a modifier: the modifier key on its own,
// then the chord. The keycast must show the chord with the modifier's name
// (Cmd on macOS, Super elsewhere) and never the modifier key itself as a key
// ("leftsuper", "leftctrl"). A modifier pressed between two presses of the
// same key must not break the repeat count.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: with CaptureKeyEvent
// recording modifier presses again, the keycast shows "leftsuper".
func TestShowKeysSkipsModifierPresses(t *testing.T) {
	term, _ := start(t, startOpts{args: []string{"--show-keys"}})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	super := "Super"
	if runtime.GOOS == "darwin" {
		super = "Cmd"
	}
	for _, k := range []string{kittyLeftSuper, kittySuperK} {
		if err := term.Type(k); err != nil {
			t.Fatalf("send %q: %v", k, err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	saveCockpitFrames(t, term, "showkeys-super")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(showkeysRow(s), super)
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the keycast does not name %s for super+k: %v\n%q\n%s", super, err, showkeysRow(term.Screen()), term.Snapshot())
	}

	// x, a lone ctrl, a lone shift, x: one pill with a count of two.
	for _, k := range []string{"x", kittyLeftCtrl, kittyLeftShift, "x"} {
		if err := term.Type(k); err != nil {
			t.Fatalf("send %q: %v", k, err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(showkeysRow(s), "x ×2")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a modifier press between two x broke the count: %v\n%q\n%s", err, showkeysRow(term.Screen()), term.Snapshot())
	}
	row := showkeysRow(term.Screen())
	saveCockpitFrames(t, term, "showkeys-modifiers")
	for _, bad := range []string{"leftsuper", "leftctrl", "leftshift", "super+k"} {
		if strings.Contains(strings.ToLower(row), bad) {
			t.Fatalf("ASSERTION: the keycast shows %q: %q\n%s", bad, row, term.Snapshot())
		}
	}
	alive(t, term, "after the modifier presses")
}
