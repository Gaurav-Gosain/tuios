package tuie2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSimpleProfileFocusFollowsMouse is #2595: the simple profile turns on
// focus_follows_mouse, and a value the file sets still wins. Two tiled panes
// side by side, the second opened with the leader. With the profile alone, a hover over the pane that does not
// have the focus moves the focus there. With focus_follows_mouse = false in
// the file, the same hover leaves the focus where it is.
//
// Negative control: drop the FocusFollowsMouse line from applyProfile. The
// "default" case then times out with the focus still on the first pane.
func TestSimpleProfileFocusFollowsMouse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		moves  bool
	}{
		{"default", "profile = \"simple\"\n", true},
		{"yours wins", "profile = \"simple\"\n\n[appearance]\nfocus_follows_mouse = false\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const session = "simplehover"
			base := t.TempDir()
			writeConfig(t, base, tc.config)
			term := startIn(t, base, startOpts{cols: 160, rows: 30, args: []string{"new", session}})
			killDaemon(t, base)
			waitBoot(t, term)
			// n on the welcome screen opens the first pane. Every key then goes
			// to the pane, so the second one comes from the leader, which the
			// simple profile keeps.
			for n, keys := range [][]any{{"n"}, {tuitest.Ctrl('b'), "c"}} {
				if err := term.SendKeys(keys...); err != nil {
					t.Fatalf("send %q: %v", keys, err)
				}
				waitUntil(t, fmt.Sprintf("pane %d opens", n+1), uiTimeout, func() (bool, string) {
					s := term.Screen()
					// The welcome box has a corner too, so it must be gone.
					return paneCount(s) == n+1 && !screenHas(s, "Terminal UI Operating System"), term.Snapshot()
				})
			}

			focused := func() string {
				wl, err := daemonWindows(base, session)
				if err != nil {
					t.Fatalf("list-windows: %v", err)
				}
				return wl.FocusedWindowID
			}
			var start string
			waitUntil(t, "the daemon names a focused window", uiTimeout, func() (bool, string) {
				start = focused()
				return start != "", term.Snapshot()
			})

			// The new pane has the focus. One of the two halves is the other pane.
			mouseHover(t, term, 40, 15)
			mouseHover(t, term, 41, 15)
			if tc.moves {
				waitUntil(t, "a hover over the other pane moves the focus", uiTimeout, func() (bool, string) {
					return focused() != start, term.Snapshot()
				})
				saveFrame(t, term, "simple-hover-focus")
				return
			}
			stayTrue(t, "with focus_follows_mouse = false a hover leaves the focus", 1500*time.Millisecond, func() (bool, string) {
				return focused() == start, term.Snapshot()
			})
		})
	}
}
