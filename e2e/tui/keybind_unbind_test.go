package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

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
// scrolling a list of a few hundred. Enter runs the row's action (see
// TestPaletteRunsTheActionItFinds), and ctrl+e opens the manager on it.
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
	if err := term.SendKeys(tuitest.Ctrl('e')); err != nil {
		t.Fatalf("rebind the action row: %v", err)
	}
	if err := term.WaitForText(keybindTitle, uiTimeout); err != nil {
		t.Fatalf("the action row did not open the manager: %v\n%s", err, term.Snapshot())
	}
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
	term, base := attachClientBase(t)

	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	waitPaletteOpen(t, term, "before unbinding")
	if err := term.Type("#toggle_zoom"); err != nil {
		t.Fatalf("type the token query: %v", err)
	}
	if err := term.WaitForText("toggle_zoom", uiTimeout); err != nil {
		t.Fatalf("no toggle_zoom row: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('e')); err != nil {
		t.Fatalf("open the manager: %v", err)
	}
	if err := term.WaitForText(keybindTitle, uiTimeout); err != nil {
		t.Fatalf("the manager did not open: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('d')); err != nil {
		t.Fatalf("send ctrl+d: %v", err)
	}
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
