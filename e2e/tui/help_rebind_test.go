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

// TestHelpListsWorkspaceKeysFromEverySection is issue #1085. The Spaces tab
// read each workspace action's keys from the first config section that named
// it. The workspaces section always does, from the defaults, so a key bound
// to switch_workspace_N in [keybindings.global] worked but never showed.
//
// How this could pass wrongly, written down first:
//   - The row could show the global key only because the test replaced the
//     default. The test binds the global key and leaves the workspaces
//     section alone, so the shipped default is still there to win first.
//   - The key could show in some other row. The test wants the row whose
//     description is "Switch to workspace 1".
func TestHelpListsWorkspaceKeysFromEverySection(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[keybindings.global]\nswitch_workspace_1 = [\"f7\"]\n")
	term := startIn(t, base, startOpts{cols: 140, rows: 44})
	waitBoot(t, term)

	openHelp(t, term)
	if err := term.SendKeys("/"); err != nil {
		t.Fatalf("send '/': %v", err)
	}
	if err := term.SendKeys("workspace 1"); err != nil {
		t.Fatalf("type search: %v", err)
	}
	shows := func(s tuitest.Screen) bool {
		for line := range strings.SplitSeq(s.Text(), "\n") {
			if strings.Contains(line, "Switch to workspace 1") && strings.Contains(line, "f7") {
				return true
			}
		}
		return false
	}
	if err := term.WaitFor(shows, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the help overlay does not list the global f7 for Switch to workspace 1: %v\n%s",
			err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "help-workspace-global-key")
}
