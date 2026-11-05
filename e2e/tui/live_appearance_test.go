package tuie2e

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// liveCols and liveRows are the size of the terminal in the report on
// issue #210, where the sidebar and the controls leave the panes little room.
const liveCols, liveRows = 84, 30

// liveAppearanceCase is one appearance option set with `tuios set-config`
// while a client is attached, and the value that puts it back.
type liveAppearanceCase struct {
	path, value, restore string
	// noDock is true when the value hides the dock, so its window count is not
	// there to read.
	noDock bool
	// changesFrame is true when the value changes what is drawn, so a frame
	// equal to the one before means the set did not land.
	changesFrame bool
	// shows is text the frame must carry after the set, when the change is
	// one a frame merely differing would not prove.
	shows string
}

// liveAppearanceCases are the options the v0.8.0 release notes and the replies
// on the issue tracker tell people to set live.
var liveAppearanceCases = []liveAppearanceCase{
	{path: "appearance.hide_window_buttons", value: "true", restore: "false", changesFrame: true},
	{path: "appearance.window_button_style", value: "pill", restore: "dots", changesFrame: true},
	{path: "appearance.window_button_position", value: "right", restore: "left", changesFrame: true},
	{path: "appearance.border_style", value: "hidden", restore: "rounded", changesFrame: true},
	{path: "appearance.border_style", value: "thick", restore: "rounded", changesFrame: true},
	{path: "appearance.window_title_position", value: "hidden", restore: "top", changesFrame: true},
	{path: "appearance.window_title_style", value: "plain", restore: "pill", changesFrame: true},
	{path: "appearance.window_title_align", value: "center", restore: "auto", changesFrame: true},
	// The attached session's name on the rail, in the pill caps.
	{path: "appearance.sidebar.row_style", value: "pill", restore: "bar", changesFrame: true, shows: "\ue0b6home\ue0b4"},
	{path: "appearance.sidebar.row_style", value: "marker", restore: "bar", changesFrame: true, shows: "›· home"},
	// The sessions header in the pill caps, and behind its icon.
	{path: "appearance.sidebar.header_style", value: "pill", restore: "auto", changesFrame: true, shows: "\ue0b6sessions\ue0b4"},
	{path: "appearance.sidebar.header_style", value: "icon", restore: "auto", changesFrame: true, shows: "\ueea8 sessions"},
	{path: "appearance.sidebar.density", value: "roomy", restore: "normal", changesFrame: true},
	{path: "appearance.shared_borders", value: "true", restore: "false", changesFrame: true},
	{path: "appearance.motion", value: "none", restore: "full"},
	{path: "appearance.glyphs", value: "unicode", restore: "default", changesFrame: true},
	{path: "appearance.zen_mode", value: "always", restore: "disabled", changesFrame: true},
	{path: "appearance.dockbar_position", value: "hidden", restore: "top", noDock: true, changesFrame: true},
}

// TestLiveAppearanceKeepsTheScreen sets each option in liveAppearanceCases on
// an attached client, from a shell outside tuios the way a person does, and
// holds:
//
//   - the panes are still drawn: both shells' prompts are on screen, and the
//     rail and (unless the value hides it) the dock's window count are too;
//   - setting the value back puts the screen back as it was before;
//   - a client that detaches and attaches again, and so reads the config
//     file the set wrote, draws the panes.
//
// Issue #210: after `set-config appearance.hide_window_buttons true` the
// whole interface was gone, and setting it back did not bring it back.
func TestLiveAppearanceKeepsTheScreen(t *testing.T) {
	for _, c := range liveAppearanceCases {
		t.Run(strings.TrimPrefix(c.path, "appearance.")+"="+c.value, func(t *testing.T) {
			base := t.TempDir()
			killDaemon(t, base)
			useShippedLooks(base)
			for _, args := range [][]string{
				{"new", "home", "--detach"},
				{"new-window", "second", "-s", "home", "--no-focus"},
			} {
				if o, err := tuiosCLI(t, base, args...); err != nil {
					t.Fatalf("%v: %v\n%s", args, err, o)
				}
			}
			term := attachIn(t, base, "home", startOpts{shippedLooks: true, animations: true, cols: liveCols, rows: liveRows})
			waitPanesDrawn(t, term, false, "before the set")
			before := stableText(t, term)

			setLive(t, base, c.path, c.value)
			after := stableText(t, term)
			if c.changesFrame && sameFrame(after, before) {
				t.Fatalf("%s=%s changed nothing on screen\n%s", c.path, c.value, after)
			}
			waitPanesDrawn(t, term, c.noDock, "after "+c.path+"="+c.value)
			if c.shows != "" && !strings.Contains(after, c.shows) {
				t.Fatalf("%s=%s: the frame does not show %q\n%s", c.path, c.value, c.shows, after)
			}
			saveArtifact(t, term, artifactDir(t), "set")

			setLive(t, base, c.path, c.restore)
			_ = stableText(t, term)
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return sameFrame(s.Text(), before)
			}, uiTimeout); err != nil {
				t.Fatalf("setting %s back to %s did not restore the screen: %v\nbefore:\n%s\nnow:\n%s",
					c.path, c.restore, err, before, term.Snapshot())
			}

			// A second set and a fresh client: what a person who detached
			// and attached again sees.
			setLive(t, base, c.path, c.value)
			waitPanesDrawn(t, term, c.noDock, "set again")
			_ = term.Close()
			again := startIn(t, base, startOpts{shippedLooks: true, animations: true, cols: liveCols, rows: liveRows, args: []string{"attach", "home"}})
			waitPanesDrawn(t, again, c.noDock, "after attaching again")
		})
	}
}

// TestSetConfigWithNoClientAppliesOnAttach sets an option while no client
// is attached, then attaches one, and holds:
//
//   - the reply says the next client applies the value;
//   - the client that attaches draws the value and writes it to config.toml;
//   - get-config reads the value back;
//   - set-config --no-save hides the rail, and a later save of another
//     option leaves the sidebar's enabled key out of config.toml while the
//     rail stays hidden.
//
// set-config with no client said "It applies when a client attaches", and a
// client that attached later never applied it. Reported by josephsintum on
// #1454. Every set-config was saved, and a value set by a hook dirtied a
// dotfiles repository. Reported on #1582.
func TestSetConfigWithNoClientAppliesOnAttach(t *testing.T) {
	const path, value, shows = "appearance.sidebar.row_style", "pill", "\ue0b6home\ue0b4"
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	if o, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("new: %v\n%s", err, o)
	}
	out, err := tuiosCLI(t, base, "set-config", path, value)
	if err != nil {
		t.Fatalf("set-config %s %s: %v\n%s", path, value, err, out)
	}
	if !strings.Contains(out, "next client that attaches applies it") {
		t.Fatalf("the reply does not say the next client applies the value:\n%s", out)
	}

	term := attachIn(t, base, "home", startOpts{shippedLooks: true, cols: liveCols, rows: liveRows})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), shows)
	}, uiTimeout); err != nil {
		t.Fatalf("the client that attached did not apply %s=%s: %v\n%s", path, value, err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "applied-on-attach")

	file := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "config.toml")
	deadline := time.Now().Add(uiTimeout)
	for {
		data, _ := os.ReadFile(file)
		if strings.Contains(string(data), `row_style = "pill"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the client did not write %s=%s to config.toml:\n%s", path, value, data)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if o, err := tuiosCLI(t, base, "get-config", path); err != nil || !strings.Contains(o, value) {
		t.Fatalf("get-config %s: %v\n%s", path, err, o)
	}

	// --no-save: the rail hides now, and no file learns it.
	if o, err := tuiosCLI(t, base, "set-config", "--no-save", "appearance.sidebar.enabled", "false"); err != nil {
		t.Fatalf("set-config --no-save: %v\n%s", err, o)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), shows)
	}, uiTimeout); err != nil {
		t.Fatalf("set-config --no-save did not hide the rail: %v\n%s", err, term.Snapshot())
	}
	// A save of another option. It writes config.toml again, and it must
	// leave the sidebar's enabled key out.
	setLive(t, base, "appearance.border_style", "double")
	deadline = time.Now().Add(uiTimeout)
	var data []byte
	for {
		data, _ = os.ReadFile(file)
		if strings.Contains(string(data), `border_style = "double"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the client did not save border_style:\n%s", data)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if keys := tableKeys(string(data), "appearance.sidebar"); slices.Contains(keys, "enabled") {
		t.Fatalf("ASSERTION: a save of border_style wrote the sidebar value set with --no-save:\n%s", data)
	}
	if strings.Contains(string(data), "sidebar_enabled") {
		t.Fatalf("ASSERTION: a save of border_style wrote sidebar_enabled:\n%s", data)
	}
	text := stableText(t, term)
	if strings.Contains(text, shows) {
		t.Fatalf("ASSERTION: the rail came back after the save:\n%s", term.Snapshot())
	}
	dir := artifactDir(t)
	saveArtifact(t, term, dir, "no-save-rail-hidden")
	saveSyncArtifact(t, "config.toml", string(data))
}

// tableKeys are the keys set directly under [table] in a config file, by
// line. It is enough for the plain files tuios writes.
func tableKeys(data, table string) []string {
	var keys []string
	in := false
	for line := range strings.SplitSeq(data, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			in = line == "["+table+"]"
			continue
		}
		if k, _, ok := strings.Cut(line, "="); in && ok && !strings.HasPrefix(line, "#") {
			keys = append(keys, strings.TrimSpace(k))
		}
	}
	return keys
}

// setLive runs `tuios set-config path value` against the daemon under base.
func setLive(t *testing.T, base, path, value string) {
	t.Helper()
	if o, err := tuiosCLI(t, base, "set-config", path, value); err != nil {
		t.Fatalf("set-config %s %s: %v\n%s", path, value, err, o)
	}
}

// waitPanesDrawn waits until both panes' prompts, the rail and the dock's
// window count are on screen.
func waitPanesDrawn(t *testing.T, term *tuitest.Terminal, noDock bool, when string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		if strings.Count(text, "$ ") < 2 || !strings.Contains(text, "sessions") {
			return false
		}
		return noDock || countWindows(s) == 2
	}, uiTimeout); err != nil {
		t.Fatalf("%s: the panes are not drawn: %v\n%s", when, err, term.Snapshot())
	}
}

// stableText is the screen's text once it has settled, with the dock's
// notification area blanked, since a set-config raises a notice there.
func stableText(t *testing.T, term *tuitest.Terminal) string {
	t.Helper()
	time.Sleep(500 * time.Millisecond)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	return term.Screen().Text()
}

// sameFrame compares two frames below the dock row, where a notice or a
// clock may differ.
func sameFrame(a, b string) bool {
	body := func(s string) string {
		lines := strings.Split(s, "\n")
		if len(lines) > 1 {
			lines = lines[1:]
		}
		return strings.Join(lines, "\n")
	}
	return body(a) == body(b)
}
