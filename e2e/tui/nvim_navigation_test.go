package tuie2e

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func TestNvimNavigationOSCNeedsActivePane(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nnvim_navigation = true\n")
	const session = "nvim-osc"
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", session}})
	killDaemon(t, base)
	waitBoot(t, term)
	for range 2 {
		newWindow(t, term)
	}
	waitWindowCount(t, term, 2, "nvim OSC setup")
	enableTiling(t, term)
	enterTerminalMode(t, term)

	_, before := focusedLayout(t, base, session)
	if err := term.SendKeys(`printf '\033]7777;tuios-nvim-navigator;focus;left\007'`, tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	_, after := focusedLayout(t, base, session)
	if after != before {
		t.Fatalf("inactive pane moved focus from %s to %s", before, after)
	}
}

func TestNvimNavigationOSCNeedsForwardedKey(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nnvim_navigation = true\n")
	const session = "nvim-osc-active"
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", session}})
	killDaemon(t, base)
	waitBoot(t, term)
	for range 2 {
		newWindow(t, term)
	}
	waitWindowCount(t, term, 2, "nvim OSC setup")
	enableTiling(t, term)
	enterTerminalMode(t, term)

	_, before := focusedLayout(t, base, session)
	if err := term.SendKeys(`printf '\033]7777;tuios-nvim-navigator;state;active\007'`, tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if err := term.SendKeys(`printf '\033]7777;tuios-nvim-navigator;focus;left\007'`, tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	_, after := focusedLayout(t, base, session)
	if after != before {
		t.Fatalf("active pane moved focus from %s to %s without a forwarded key", before, after)
	}

	keyFile := filepath.Join(base, "nav-key")
	if err := term.SendKeys(`stty raw -echo; printf 'NAV-%s-READY\r\n' $((3*7)); dd bs=64 count=1 of=`+keyFile+` 2>/dev/null; printf '\033]7777;tuios-nvim-navigator;focus;left\007'; stty sane`, tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("NAV-21-READY", shellTimeout); err != nil {
		t.Fatalf("the pane never got ready for the key: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("\x1b[1;3D"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, after = focusedLayout(t, base, session); after != before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("focus stayed on %s after a forwarded key and the pane's focus request", before)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if key, err := os.ReadFile(keyFile); err != nil || len(key) == 0 {
		t.Fatalf("the focus key never reached the active pane (read %q, %v)", key, err)
	}
}

// TestNvimNavigationUpDownUnderOlderNames is issue #2158. v0.13.2 renamed
// terminal_focus_up and terminal_focus_down to focus_up and focus_down. The
// Neovim navigator takes its direction from the action name, and it cut the
// prefix "terminal_focus_" off a name that no longer has it. A focus key then
// armed the navigator for the direction "focus_up", the pane's reply said
// "up", the two never matched, and the key did nothing. Left and right kept
// their names and kept working, so only up and down broke.
//
// The config binds the keys under the older names, as the report does. A
// shell in the focused pane plays the Neovim plugin: it says the navigator is
// active, reads the forwarded key, and answers with a focus request.
//
// How this could pass wrongly, written down first:
//   - tuios could move focus on the key itself, with no reply. The shell
//     keeps the key it read, and the test wants that key.
//   - The direction could be one left and right share. The test uses up or
//     down only, the two that lost their old names.
//
// NEGATIVE CONTROL: with nvimNavigationDirection back to TrimPrefix on
// "terminal_focus_", focus stays on the first pane and the test fails.
func TestNvimNavigationUpDownUnderOlderNames(t *testing.T) {
	const session = "nvim-updown"
	term, base := tiledPanesWith(t, session, "",
		"[appearance]\nnvim_navigation = true\n\n"+
			"[keybindings.terminal_mode]\nterminal_focus_up = [\"alt+k\"]\nterminal_focus_down = [\"alt+j\"]\n", 3)
	waitForSettledGeometryIn(t, base, session, 3)
	layout, before := focusedLayout(t, base, session)
	var from winRect
	for _, r := range layout {
		if r.ID == before {
			from = r.winRect
		}
	}
	dir, key := "up", "\x1bk"
	if len(touchingNeighbours(from, layout, dir)) == 0 {
		dir, key = "down", "\x1bj"
	}
	if len(touchingNeighbours(from, layout, dir)) == 0 {
		t.Fatalf("the focused pane %s has no pane above or below it: %+v", before[:8], layout)
	}
	enterTerminalMode(t, term)

	keyFile := filepath.Join(base, "nav-key")
	osc := func(body string) string { return `printf '\033]7777;tuios-nvim-navigator;` + body + `\007'` }
	line := osc("state;active") + `; stty raw -echo; printf 'NAV-%s-READY\r\n' $((3*7)); dd bs=64 count=1 of=` + keyFile +
		` 2>/dev/null; ` + osc("focus;"+dir) + `; stty sane`
	if err := term.SendKeys(line, tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("NAV-21-READY", shellTimeout); err != nil {
		t.Fatalf("the pane never got ready for the key: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(key); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "focus leaves the pane after the forwarded focus_"+dir+" key and the pane's focus;"+dir+" reply", 10*time.Second, func() (bool, string) {
		_, after := focusedLayout(t, base, session)
		return after != before, "focus is on " + after
	})
	if got, err := os.ReadFile(keyFile); err != nil || len(got) == 0 {
		t.Fatalf("ASSERTION: the focus key never reached the pane (read %q, %v): tuios moved focus on the key itself", got, err)
	}
	saveArtifact(t, term, artifactDir(t), "nvim-"+dir+"-moved")
}
