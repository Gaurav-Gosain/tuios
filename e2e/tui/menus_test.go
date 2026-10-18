package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// menusConfig declares what a tmux which-key tree would: a split menu under
// the window prefix, and inside it a sticky resize menu. Nothing here ships.
const menusConfig = `
[keybindings.window_prefix]
"open_menu:split" = ["s"]

[keybindings.menus.split]
title = "Split"

[keybindings.menus.split.keys]
prefix_split_vertical = ["y"]
"open_menu:resize" = ["R"]

[keybindings.menus.resize]
title = "Resize"
sticky = true

[keybindings.menus.resize.keys]
resize_pane_left_5 = ["h"]
`

// TestDeclaredMenuSplitsFromTheLeader: leader t s y reaches a menu the config
// declared and runs the split bound in it, from window mode and from terminal
// mode, which route the open menu through two copies of one rule.
//
// Negative control: delete the `if o.Menu != ""` block in routeKey (window
// mode) or in HandleTerminalModeKey (terminal mode), or the
// config.OpenMenuPrefix case in ActionDispatcher.Dispatch. The chord dies at
// that link and the pane count stays at 1.
func TestDeclaredMenuSplitsFromTheLeader(t *testing.T) {
	for _, tc := range []struct {
		name         string
		terminalMode bool
	}{{"window mode", false}, {"terminal mode", true}} {
		t.Run(tc.name, func(t *testing.T) {
			term, base := panesSession(t, "menus-split", menusConfig)
			waitForSettledGeometryIn(t, base, "menus-split", 1)
			if tc.terminalMode {
				enterTerminalMode(t, term)
			}
			if err := term.SendKeys(tuitest.Ctrl('b'), "t", "s", "y"); err != nil {
				t.Fatalf("send leader t s y: %v", err)
			}
			waitForSettledGeometryIn(t, base, "menus-split", 2)
		})
	}
}

// TestDeclaredStickyMenuStaysOpenUntilAnUnboundKey: leader t s R h h narrows
// the focused pane twice on one chord, with the which-key panel listing the
// menu's keys while it is open. z, which the menu does not bind, closes it, so
// the h after it is the ordinary focus key and the width stops changing. The
// two h presses are the positive half: the pane does shrink, so the unchanged
// width at the end is the menu closing and not a resize that never worked.
//
// Negative control: delete the sticky re-open (`m.Menu = name`) in
// HandleMenuCommand. The first h resizes and closes the menu, and the second h
// is a focus key, so the width does not shrink a second time.
func TestDeclaredStickyMenuStaysOpenUntilAnUnboundKey(t *testing.T) {
	term, base := panesSession(t, "menus-sticky", menusConfig)
	waitForSettledGeometryIn(t, base, "menus-sticky", 1)
	b := newWindowIn(t, base, "menus-sticky")
	waitForSettledGeometryIn(t, base, "menus-sticky", 2)
	width := func() int { return listPanes(t, base, "menus-sticky")[b].Width }
	w0 := width()

	if err := term.SendKeys(tuitest.Ctrl('b'), "t", "s", "R"); err != nil {
		t.Fatalf("send leader t s R: %v", err)
	}
	if err := term.WaitForText("Narrow the focused pane by 5", uiTimeout); err != nil {
		t.Fatalf("the resize menu's panel never showed: %v\n%s", err, term.Snapshot())
	}
	waitShrinks := func(from int, what string) int {
		t.Helper()
		var got int
		deadline := time.Now().Add(uiTimeout)
		for time.Now().Before(deadline) {
			if got = width(); got < from {
				return got
			}
			time.Sleep(150 * time.Millisecond)
		}
		t.Fatalf("ASSERTION: %s: pane b is still %d wide, want less than %d", what, got, from)
		return got
	}
	if err := term.SendKeys("h"); err != nil {
		t.Fatalf("send h: %v", err)
	}
	w1 := waitShrinks(w0, "the first h")
	if err := term.SendKeys("h"); err != nil {
		t.Fatalf("send second h: %v", err)
	}
	w2 := waitShrinks(w1, "the second h, on the same chord")

	if err := term.SendKeys("z", "h"); err != nil {
		t.Fatalf("send z h: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if got := width(); got != w2 {
		t.Fatalf("pane b is %d wide after z h, want %d: the menu was still open", got, w2)
	}
}
