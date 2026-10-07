package tuie2e

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// whichKeyKeyOf returns the key which-key shows in front of desc, or "" when
// no row says desc. The key is the word just before the description.
func whichKeyKeyOf(s tuitest.Screen, desc string) string {
	re := regexp.MustCompile(`(\S+)\s+` + regexp.QuoteMeta(desc) + `(\s|$)`)
	for line := range strings.SplitSeq(s.Text(), "\n") {
		if m := re.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

// whichKeyRebindConfig rebinds the workspace menu's rename key, unbinds its
// fifth switch, and moves the leader's zoom key. Empty values keep the
// defaults.
func whichKeyRebindConfig(themeName, rename, zoom string, unbindFifth bool) string {
	cfg := "[appearance]\n"
	if themeName != "" {
		cfg += "theme = \"" + themeName + "\"\n"
	}
	cfg += "\n[keybindings.workspace_prefix]\n"
	if rename != "" {
		cfg += "workspace_prefix_rename = [\"" + rename + "\"]\n"
	}
	if unbindFifth {
		cfg += "workspace_prefix_switch_5 = []\n"
	}
	if zoom != "" {
		cfg += "\n[keybindings.prefix_mode]\nprefix_fullscreen = [\"" + zoom + "\"]\n"
	}
	return cfg
}

// TestWhichKeyShowsTheConfiguredKeys: every which-key menu reads its keys from
// the config. The sub-prefix menus were hand-written key lists, so a key
// rebound in config.toml went on showing its default there, and an unbound key
// went on being offered.
//
// The workspace menu is opened with rename moved from r to e and the fifth
// switch unbound: the rename row shows e and not r, and the switch row reads
// 1-4/6-9. The leader's zoom key moves from z to Z. The config is then saved
// again with rename on y and the switch bound, as an editor saves it, and the
// open tuios shows y and 1-9 with no restart. Each step asserts the new key
// and the absence of the old one, so the positive half (the default key
// showing) is the old key of the next step. A dark and a light theme each
// leave a frame of the menu under the artifact directory.
//
// Negative control: in internal/app/render_whichkey.go, build the workspace
// menu from the shipped keymap (pass nil for the registry in whichKeyMenu's
// menu helper). Both subtests fail: the rename row shows r. Dropping
// `r.live = nil` from the registry's buildMappings fails both at the runtime
// rebind: the row keeps e. See NEGATIVE_CONTROLS.md.
func TestWhichKeyShowsTheConfiguredKeys(t *testing.T) {
	for _, look := range []struct{ name, theme string }{
		{"dark", ""},
		{"light", "catppuccin_latte"},
	} {
		t.Run(look.name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, whichKeyRebindConfig(look.theme, "e", "Z", true))
			term := startIn(t, base, startOpts{cols: 120, rows: 36})
			waitBoot(t, term)
			dir := artifactDir(t)
			host := hostPalette(t, look.theme)

			openWorkspaceMenu := func() {
				t.Helper()
				sendKeys(t, term, tuitest.Ctrl('b'), "w")
				waitScreen(t, term, "the workspace menu never drew", "Rename workspace", "Switch to workspace")
			}
			closeMenu := func() {
				t.Helper()
				sendKeys(t, term, tuitest.Esc)
				waitGone(t, term, "the which-key menu", "Rename workspace")
			}
			waitKeys := func(what string, want map[string]string) {
				t.Helper()
				if err := term.WaitFor(func(s tuitest.Screen) bool {
					for desc, key := range want {
						if whichKeyKeyOf(s, desc) != key {
							return false
						}
					}
					return true
				}, configWatchTimeout); err != nil {
					s := term.Screen()
					for desc, key := range want {
						t.Errorf("%s: the row %q shows key %q, want %q", what, desc, whichKeyKeyOf(s, desc), key)
					}
					t.Fatalf("%s\n%s", what, term.Snapshot())
				}
			}

			// The rebound and unbound keys, from the config read at start.
			openWorkspaceMenu()
			waitKeys("the workspace menu after a rebind", map[string]string{
				"Rename workspace":    "e",
				"Switch to workspace": "1-4/6-9",
			})
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("the menu never settled: %v", err)
			}
			saveArtifact(t, term, dir, "workspace-rebound")
			savePNG(t, term.Screen(), host, dir, "workspace-rebound")
			closeMenu()

			// The leader's own menu reads the config too.
			sendKeys(t, term, tuitest.Ctrl('b'))
			waitScreen(t, term, "the leader menu never drew", "Toggle zoom", "+Workspace")
			waitKeys("the leader menu after a rebind", map[string]string{"Toggle zoom": "Z"})
			sendKeys(t, term, tuitest.Esc)
			waitGone(t, term, "the leader menu", "Toggle zoom")

			// Saved again while tuios runs: the menu follows the new file.
			saveConfigLikeAnEditor(t, base, whichKeyRebindConfig(look.theme, "y", "", false))
			openWorkspaceMenu()
			waitKeys("the workspace menu after a runtime rebind", map[string]string{
				"Rename workspace":    "y",
				"Switch to workspace": "1-9",
			})
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("the menu never settled: %v", err)
			}
			saveArtifact(t, term, dir, "workspace-runtime-rebind")
			savePNG(t, term.Screen(), host, dir, "workspace-runtime-rebind")
			closeMenu()

			sendKeys(t, term, tuitest.Ctrl('b'))
			waitScreen(t, term, "the leader menu never drew", "Toggle zoom", "+Workspace")
			waitKeys("the leader menu after the zoom key went back to its default", map[string]string{"Toggle zoom": "z"})
			sendKeys(t, term, tuitest.Esc)
			waitGone(t, term, "the leader menu", "Toggle zoom")
			alive(t, term, "after rebinding the which-key menus")
		})
	}
}

// TestWhichKeyRowsAreTheKeysThatRun: a which-key row names the key that runs
// its action, and a row is gone when its key does nothing. The window menu is
// opened with new window moved from n to y and close window unbound. The menu
// shows y for the new window and no close row, y opens a pane, and the old n
// and the unbound x do nothing.
//
// Negative control: build the window menu from the shipped keymap (pass nil
// for the registry in whichKeyMenu's menu helper in
// internal/app/render_whichkey.go). The new window row shows n, and the close
// row comes back.
func TestWhichKeyRowsAreTheKeysThatRun(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[keybindings.window_prefix]\nwindow_prefix_new = [\"y\"]\nwindow_prefix_close = []\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 36})
	waitBoot(t, term)
	// The dock's "1:<windows>" readout is the count of windows on workspace 1.
	readout := regexp.MustCompile(`\b1:(\d+)\b`)
	frames := func(s tuitest.Screen) int {
		m := readout.FindStringSubmatch(s.Text())
		if m == nil {
			return -1
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}
	panes := func() int { return frames(term.Screen()) }
	before := panes()
	if before < 0 {
		t.Fatalf("the dock shows no window count\n%s", term.Snapshot())
	}

	sendKeys(t, term, tuitest.Ctrl('b'), "t")
	waitScreen(t, term, "the window menu never drew", "New window", "Toggle tiling mode")
	s := term.Screen()
	if got := whichKeyKeyOf(s, "New window"); got != "y" {
		t.Fatalf("the new window row shows key %q, want y\n%s", got, term.Snapshot())
	}
	if strings.Contains(s.Text(), "Close window") {
		t.Fatalf("the window menu offers the unbound close key\n%s", term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "window-menu-rebound")

	// The key the row names runs the action.
	sendKeys(t, term, "y")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return frames(s) == before+1 }, uiTimeout); err != nil {
		t.Fatalf("the y the menu shows did not open a window: %d windows, want %d\n%s", panes(), before+1, term.Snapshot())
	}

	// The old key and the unbound key do nothing.
	for _, key := range []string{"n", "x"} {
		sendKeys(t, term, tuitest.Ctrl('b'), "t")
		waitScreen(t, term, "the window menu never drew", "New window")
		sendKeys(t, term, key)
		waitGone(t, term, "the which-key menu", "Toggle tiling mode")
		if err := term.WaitStable(uiTimeout); err != nil {
			t.Fatalf("the screen never settled: %v", err)
		}
		if got := panes(); got != before+1 {
			t.Fatalf("ctrl+b t %s changed the windows: %d windows, want %d\n%s", key, got, before+1, term.Snapshot())
		}
	}
	alive(t, term, "after the rebound window menu")
}
