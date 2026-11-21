package tuie2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestThemeEventsFollowThePicker subscribes to theme events, browses three
// themes in the theme picker with the arrow keys, and picks the third. A
// follower must see each theme the picker shows, marked as a preview, and
// then the pick once with preview false, in that order. Opening the picker on
// the theme in force sends nothing.
//
// Negative control: with noteThemeEvent returning while the picker is open,
// as the after-theme-change hook does, the three previews are missing and the
// test fails at the first wait.
func TestThemeEventsFollowThePicker(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[appearance]\ntheme = \"tokyonight\"\n")
	if out, err := tuiosCLI(t, base, "new", "e2e-theme", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-theme"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Alt(tuitest.Esc))
	waitWindowMode(t, term)
	read := subscribeLines(t, base, "--types", "theme")

	type themeEv struct {
		Type    string `json:"type"`
		Session string `json:"session"`
		Theme   struct {
			ID         string `json:"id"`
			Background string `json:"background"`
			Light      *bool  `json:"light"`
			Preview    bool   `json:"preview"`
		} `json:"theme"`
	}
	events := func() ([]themeEv, string) {
		lines := read()
		var out []themeEv
		for _, l := range lines {
			var ev themeEv
			if err := json.Unmarshal([]byte(l), &ev); err != nil {
				t.Fatalf("an event line is not JSON: %q", l)
			}
			out = append(out, ev)
		}
		return out, strings.Join(lines, "\n")
	}
	waitEvents := func(what string, n int) []themeEv {
		t.Helper()
		var got []themeEv
		waitUntil(t, what, uiTimeout, func() (bool, string) {
			var state string
			got, state = events()
			return len(got) >= n, state
		})
		return got
	}

	sendKeys(t, term, legacyCtrlP)
	waitScreen(t, term, "the palette never opened", paletteTitle)
	sendKeys(t, term, "Theme picker")
	waitScreen(t, term, "the palette never filtered", "Theme picker")
	sendKeys(t, term, tuitest.Enter)
	waitScreen(t, term, "the theme picker never opened", "themes")
	stayTrue(t, "opening the picker on the theme in force sent an event", time.Second, func() (bool, string) {
		got, state := events()
		return len(got) == 0, state
	})

	for i := 1; i <= 3; i++ {
		sendKeys(t, term, tuitest.Down)
		waitEvents(fmt.Sprintf("preview %d did not arrive", i), i)
	}
	saveArtifact(t, term, artifactDir(t), "third-preview")
	closeThemePicker(t, term, "the third theme", tuitest.Enter)
	got := waitEvents("the pick did not arrive", 4)
	stayTrue(t, "more events than the three previews and the pick", time.Second, func() (bool, string) {
		all, state := events()
		return len(all) == 4, state
	})

	_, raw := events()
	seen := map[string]bool{"tokyonight": true}
	for i, ev := range got {
		if ev.Type != "theme" || ev.Session != "e2e-theme" {
			t.Errorf("event %d is %q in session %q, want theme in e2e-theme\n%s", i, ev.Type, ev.Session, raw)
		}
		if len(ev.Theme.Background) != 7 || ev.Theme.Background[0] != '#' || ev.Theme.Light == nil {
			t.Errorf("event %d has background %q and light %v, want #rrggbb and a flag\n%s", i, ev.Theme.Background, ev.Theme.Light, raw)
		}
		if i < 3 {
			if !ev.Theme.Preview || seen[ev.Theme.ID] {
				t.Errorf("event %d is %q preview %v, want a new theme as a preview\n%s", i, ev.Theme.ID, ev.Theme.Preview, raw)
			}
			seen[ev.Theme.ID] = true
		}
	}
	if pick := got[3].Theme; pick.Preview || pick.ID != got[2].Theme.ID {
		t.Errorf("the last event is %q preview %v, want the third preview %q with preview false\n%s", pick.ID, pick.Preview, got[2].Theme.ID, raw)
	}
	t.Logf("events:\n%s", raw)
}
