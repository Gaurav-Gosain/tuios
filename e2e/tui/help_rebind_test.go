package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// newWindowOn is a config that binds new_window, and nothing else, to key.
func newWindowOn(key string) string {
	return "[keybindings.window_management]\nnew_window = [\"" + key + "\"]\n"
}

// helpRowShows reports whether the help overlay has a "New window" row that
// lists key.
func helpRowShows(s tuitest.Screen, key string) bool {
	for line := range strings.SplitSeq(s.Text(), "\n") {
		if strings.Contains(line, "New window") && strings.Contains(line, key) {
			return true
		}
	}
	return false
}

// TestHelpShowsAKeyReboundAtRuntime: the help overlay keeps the table of keys
// it lists between frames, so it has to drop that table when the keys change.
// A key rebound in config.toml while tuios runs must show in the overlay that
// is already open, and the old key must leave it.
func TestHelpShowsAKeyReboundAtRuntime(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, newWindowOn("f9"))
	term := startIn(t, base, startOpts{cols: 140, rows: 44})
	waitBoot(t, term)

	openHelp(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return helpRowShows(s, "f9") }, uiTimeout); err != nil {
		t.Fatalf("the help overlay does not list the configured f9 for New window: %v\n%s",
			err, term.Snapshot())
	}

	saveConfigLikeAnEditor(t, base, newWindowOn("f8"))
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return helpRowShows(s, "f8") && !helpRowShows(s, "f9")
	}, configWatchTimeout); err != nil {
		t.Fatalf("the open help overlay still lists the old key after a rebind: %v\n%s",
			err, term.Snapshot())
	}

	// Closed and opened again, it still shows the new key.
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), helpTitle) }, uiTimeout); err != nil {
		t.Fatalf("help overlay did not close: %v\n%s", err, term.Snapshot())
	}
	openHelp(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return helpRowShows(s, "f8") && !helpRowShows(s, "f9")
	}, uiTimeout); err != nil {
		t.Fatalf("the reopened help overlay does not list the rebound key: %v\n%s",
			err, term.Snapshot())
	}
	alive(t, term, "after a rebind with help open")
}
