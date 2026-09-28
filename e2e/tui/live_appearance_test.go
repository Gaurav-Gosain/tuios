package tuie2e

import (
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
	{path: "appearance.shared_borders", value: "true", restore: "false", changesFrame: true},
	{path: "appearance.outer_gap", value: "2", restore: "0", changesFrame: true},
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
