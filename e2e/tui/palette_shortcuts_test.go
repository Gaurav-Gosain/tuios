package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// paletteRowShortcut returns the shortcut the palette shows on the row named
// name, and whether the row is on screen. A palette row is the name, the
// shortcut (when there is one) and then the category, left to right.
//
// Only the lines under the search line's rule are rows. The search line above
// the rule holds the typed name too, and its caret is a reverse-video cell
// with no glyph of its own, so nothing on that line tells it apart from a row.
// The panel's frame (│) is not part of any field.
func paletteRowShortcut(s tuitest.Screen, name, category string) (string, bool) {
	lines := strings.Split(s.Text(), "\n")
	rule := -1
	for i, line := range lines {
		if !strings.Contains(line, name) {
			continue
		}
		// The first line with the name is the search line; its rule is the
		// next line drawn as a rule.
		for j := i + 1; j < len(lines); j++ {
			if strings.Contains(lines[j], "──────────") {
				rule = j
				break
			}
		}
		break
	}
	if rule < 0 {
		return "", false
	}
	for _, line := range lines[rule+1:] {
		_, after, ok := strings.Cut(line, name)
		if !ok {
			continue
		}
		fields := strings.Fields(strings.ReplaceAll(after, "│", " "))
		if len(fields) == 0 {
			continue
		}
		if fields[0] == category {
			return "", true
		}
		return fields[0], true
	}
	return "", false
}

// TestPaletteShortcutsFollowKeybinds: the palette's "Run a program" row shows
// the key the config binds the launcher to now (issue #634). The row used to
// say alt+space whatever the config said.
//
// With the shipped config the row shows the global alt+space. The config is
// then saved as the reporter wrote it, with ctrl+space as the leader and the
// global launcher key unbound, and the open tuios shows prefix+a with no
// restart. Unbinding the prefix key as well leaves the row with no shortcut.
// Each step leaves a frame of the palette under the artifact directory.
//
// Negative control: drop the m.liveShortcuts call from rebuildPaletteItems in
// internal/app/command_palette_nav.go. The second step fails: the row still
// shows alt+space. See NEGATIVE_CONTROLS.md.
func TestPaletteShortcutsFollowKeybinds(t *testing.T) {
	const row, category = "Run a program", "Run"
	base := t.TempDir()
	writeConfig(t, base, "")
	term := startIn(t, base, startOpts{cols: 120, rows: 36})
	waitBoot(t, term)
	dir := artifactDir(t)

	// expect opens the palette, looks for the row, and closes it again until
	// the row shows want. A saved config reaches the client after the
	// watcher's debounce, and the palette reads the keys when it opens.
	expect := func(step, want string) {
		t.Helper()
		deadline := time.Now().Add(configWatchTimeout)
		for {
			sendKeys(t, term, tuitest.Ctrl('p'))
			waitPaletteOpen(t, term, step)
			sendKeys(t, term, row)
			var got string
			var found bool
			err := term.WaitFor(func(s tuitest.Screen) bool {
				got, found = paletteRowShortcut(s, row, category)
				return found
			}, uiTimeout)
			if err != nil {
				t.Fatalf("%s: the palette has no %q row\n%s", step, row, term.Snapshot())
			}
			if got == want {
				if err := term.WaitStable(uiTimeout); err != nil {
					t.Fatalf("%s: the palette never settled: %v", step, err)
				}
				saveArtifact(t, term, dir, step)
				closePalette(t, term, step)
				return
			}
			if time.Now().After(deadline) {
				saveArtifact(t, term, dir, step+"-failed")
				t.Fatalf("%s: the %q row shows %q, want %q\n%s", step, row, got, want, term.Snapshot())
			}
			closePalette(t, term, step)
			time.Sleep(500 * time.Millisecond)
		}
	}

	expect("default", "alt+space")

	saveConfigLikeAnEditor(t, base, "[keybindings]\nleader_key = \"ctrl+space\"\n\n[keybindings.global]\nlauncher = []\n")
	expect("global-unbound", "prefix+a")

	saveConfigLikeAnEditor(t, base, "[keybindings]\nleader_key = \"ctrl+space\"\n\n[keybindings.global]\nlauncher = []\n\n[keybindings.prefix_mode]\nlauncher = []\n")
	expect("all-unbound", "")

	alive(t, term, "after the palette followed the keybinds")
}
