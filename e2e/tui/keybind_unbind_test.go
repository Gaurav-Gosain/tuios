package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// Unbinding is a gesture on a screen, and the unit tests only prove the model
// agrees with itself. These drive a real client through a real terminal and
// then read the file on disk, which is the only place the whole path shows up:
// palette, overlay, key handler, config writer.

// TestPaletteReachesTheKeybindManager. The manager was reachable only from the
// leader chord, which is the one place a user who does not know the chord
// cannot look.
//
// Negative control: remove the "Keybind manager" palette row and this fails at
// the overlay wait, because the palette filters to nothing.
func TestPaletteReachesTheKeybindManager(t *testing.T) {
	term := attachClient(t)
	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	waitPaletteOpen(t, term, "before finding the keybind row")

	if err := term.Type("keybind manager"); err != nil {
		t.Fatalf("type the query: %v", err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("activate the row: %v", err)
	}
	if err := term.WaitForText(keybindTitle, uiTimeout); err != nil {
		t.Fatalf("the keybind manager did not open from the palette: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after opening the keybind manager from the palette")
}

// TestPaletteTokenSearchesActions. Typing "#" turns the palette into a search
// over actions, which is what makes rebinding a specific one reachable without
// scrolling a list of a few hundred.
//
// Negative control: drop the "#" branch from FilterCommandPalette and this
// fails: "#toggle_zoom" matches no command name and the palette shows nothing.
func TestPaletteTokenSearchesActions(t *testing.T) {
	term := attachClient(t)
	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	waitPaletteOpen(t, term, "before the token search")

	if err := term.Type("#toggle_zoom"); err != nil {
		t.Fatalf("type the token query: %v", err)
	}
	if err := term.WaitForText("toggle_zoom", uiTimeout); err != nil {
		t.Fatalf("the token search found no toggle_zoom row: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("activate the action row: %v", err)
	}
	if err := term.WaitForText(keybindTitle, uiTimeout); err != nil {
		t.Fatalf("the action row did not open the manager: %v\n%s", err, term.Snapshot())
	}
	// Opened filtered to that action, so the list is that action's bindings and
	// not the top of the whole list.
	if err := term.WaitForText("Toggle zoom", uiTimeout); err != nil {
		t.Fatalf("the manager did not open on the action that was picked: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after the token search")
}

// TestUnbindingFromTheOverlayReachesTheConfigFile is the whole path: press
// ctrl+d on a binding and find an empty list in config.toml afterwards.
//
// The file is what is asserted on, not the screen. An unbind that only ever
// existed in memory is one the next restart undoes, and that is exactly the bug
// the empty-list encoding exists to prevent.
//
// Negative control: have UnbindKey delete the action rather than empty it and
// this fails, because config.toml then has no close_window line at all and the
// default comes back at the next load.
func TestUnbindingFromTheOverlayReachesTheConfigFile(t *testing.T) {
	// attachClientBase, rather than a hand-rolled attach, because it also
	// settles the client and returns the isolation root the config is written
	// into. A client typed at before it has settled drops the keystrokes.
	term, base := attachClientBase(t)

	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	waitPaletteOpen(t, term, "before unbinding")
	// One action, so the row under the cursor when the manager opens is known.
	if err := term.Type("#toggle_zoom"); err != nil {
		t.Fatalf("type the token query: %v", err)
	}
	if err := term.WaitForText("toggle_zoom", uiTimeout); err != nil {
		t.Fatalf("no toggle_zoom row: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("open the manager: %v", err)
	}
	if err := term.WaitForText(keybindTitle, uiTimeout); err != nil {
		t.Fatalf("the manager did not open: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('d')); err != nil {
		t.Fatalf("send ctrl+d: %v", err)
	}
	// The notification names what was taken, which is also how this waits for
	// the write rather than sleeping on it.
	if err := term.WaitForText("Unbound", uiTimeout); err != nil {
		t.Fatalf("nothing was reported after ctrl+d: %v\n%s", err, term.Snapshot())
	}

	path := filepath.Join(base, "XDG_CONFIG_HOME", "tuios", "config.toml")
	if err := term.WaitFor(func(tuitest.Screen) bool {
		data, err := os.ReadFile(path) // #nosec G304 - the test's own isolation root
		return err == nil && strings.Contains(string(data), "toggle_zoom = []")
	}, uiTimeout); err != nil {
		data, readErr := os.ReadFile(path) // #nosec G304
		t.Fatalf("config.toml never recorded the unbind (%v, read %v):\n%s", err, readErr, keybindTable(string(data)))
	}
	alive(t, term, "after unbinding from the overlay")
}

// keybindTable is the window_management table out of a rendered config, for a
// failure message that shows the relevant lines rather than the whole file.
func keybindTable(file string) string {
	const head = "[keybindings.window_management]"
	i := strings.Index(file, head)
	if i < 0 {
		return "no " + head + " in the file"
	}
	rest := file[i:]
	if j := strings.Index(rest[len(head):], "\n["); j >= 0 {
		rest = rest[:len(head)+j]
	}
	return rest
}
