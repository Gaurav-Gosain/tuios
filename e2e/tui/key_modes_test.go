package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const keyModesConfig = `
[keybindings.prefix_mode]
"enter_mode:resize" = ["y"]
"enter_mode:pass" = ["Y"]

[keybindings.modes.resize]
title = "Resize"
exclusive = true

[keybindings.modes.resize.keys]
resize_pane_left_5 = ["h"]
"enter_mode:split" = ["s"]

[keybindings.modes.split]
title = "Split"
exclusive = true
once = true

[keybindings.modes.split.keys]
split_vertical = ["y"]

[keybindings.modes.pass]
title = "Pass"

[keybindings.modes.pass.keys]
split_vertical = ["alt+y"]
`

// TestKeyModeStaysOnStacksAndLeavesOnEsc: leader y turns on the exclusive
// resize mode from terminal mode. The dock's chip names it and its leave
// key, and h narrows the pane twice: the mode stays on through the keys it
// runs. A key it does not bind (Z) is dropped and does not reach the shell.
// s turns on the split mode over it, and the chip names both. y splits once
// and the once mode turns off, so the chip names resize alone. esc turns
// resize off, the chip goes, and h is a letter for the shell again: the
// width stays as it was.
//
// Negative control: delete the routeKeyMode call in routeKey
// (internal/input/handler.go). The chip still shows, since the mode is on,
// but h goes to the shell and the first width wait times out.
func TestKeyModeStaysOnStacksAndLeavesOnEsc(t *testing.T) {
	const session = "keymodes"
	term, base := panesSession(t, session, keyModesConfig)
	waitForSettledGeometryIn(t, base, session, 1)
	b := newWindowIn(t, base, session)
	waitForSettledGeometryIn(t, base, session, 2)
	enterTerminalMode(t, term)
	width := func() int { return listPanes(t, base, session)[b].Width }
	w0 := width()

	if err := term.SendKeys(tuitest.Ctrl('b'), "y"); err != nil {
		t.Fatalf("send leader y: %v", err)
	}
	if err := term.WaitForText("esc leave", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the dock never showed the mode chip: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), "Resize") {
		t.Fatalf("ASSERTION: the chip does not name the mode\n%s", term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "config problem") {
		t.Fatalf("ASSERTION: the config has problems\n%s", term.Snapshot())
	}
	saveFrame(t, term, "keymodes-resize-on")

	waitShrinks := func(from int, what string) int {
		t.Helper()
		var got int
		deadline := time.Now().Add(uiTimeout)
		for time.Now().Before(deadline) {
			if got = width(); got < from {
				return got
			}
			pause(t, "poll the pane width again until the mode resizes it", 150*time.Millisecond)
		}
		t.Fatalf("ASSERTION: %s: pane b is still %d wide, want less than %d\n%s", what, got, from, term.Snapshot())
		return got
	}
	if err := term.SendKeys("h"); err != nil {
		t.Fatalf("send h: %v", err)
	}
	w1 := waitShrinks(w0, "the first h")
	if err := term.SendKeys("h"); err != nil {
		t.Fatalf("send second h: %v", err)
	}
	waitShrinks(w1, "the second h, with the mode still on")
	if err := term.SendKeys("Z"); err != nil {
		t.Fatalf("send Z: %v", err)
	}

	if err := term.SendKeys("s"); err != nil {
		t.Fatalf("send s: %v", err)
	}
	if err := term.WaitForText("Resize > Split", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the chip never named both modes: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "keymodes-stacked")
	if err := term.SendKeys("y"); err != nil {
		t.Fatalf("send y: %v", err)
	}
	waitForSettledGeometryIn(t, base, session, 3)
	waitGone(t, term, "the once mode after y", "Resize > Split")
	if !strings.Contains(term.Screen().Text(), "esc leave") {
		t.Fatalf("ASSERTION: resize turned off with the once mode above it\n%s", term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	waitGone(t, term, "the mode after esc", "esc leave")
	saveFrame(t, term, "keymodes-off")
	settled := width()
	if err := term.SendKeys("h"); err != nil {
		t.Fatalf("send h after esc: %v", err)
	}
	pause(t, "nothing on screen says a resize did not happen", 700*time.Millisecond)
	if got := width(); got != settled {
		t.Fatalf("ASSERTION: h resized the pane after esc: %d, want %d", got, settled)
	}
	if strings.Contains(term.Screen().Text(), "Zh") {
		t.Fatalf("ASSERTION: the exclusive mode let Z reach the shell\n%s", term.Snapshot())
	}
}

// TestKeyModeNotExclusivePassesKeysToThePane: leader Y turns on pass, which
// binds only alt+y. Text typed with the mode on reaches the shell, which
// runs it, and alt+y splits. esc turns the mode off.
//
// Negative control: delete the routeKeyMode call in routeKey. The echo
// still reaches the shell, which is the half a broken mode must not break,
// but alt+y never splits and the wait for two panes times out.
func TestKeyModeNotExclusivePassesKeysToThePane(t *testing.T) {
	const session = "keymodes-pass"
	term, base := panesSession(t, session, keyModesConfig)
	waitForSettledGeometryIn(t, base, session, 1)
	enterTerminalMode(t, term)

	if err := term.SendKeys(tuitest.Ctrl('b'), "Y"); err != nil {
		t.Fatalf("send leader Y: %v", err)
	}
	if err := term.WaitForText("Pass", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the dock never showed the mode chip: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("echo KEYMODE$((40+2))", tuitest.Enter); err != nil {
		t.Fatalf("type echo: %v", err)
	}
	if err := term.WaitForText("KEYMODE42", shellTimeout); err != nil {
		t.Fatalf("ASSERTION: text typed in a mode that is not exclusive did not reach the shell: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt('y')); err != nil {
		t.Fatalf("send alt+y: %v", err)
	}
	waitForSettledGeometryIn(t, base, session, 2)
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	waitGone(t, term, "the mode after esc", "esc leave")
}
