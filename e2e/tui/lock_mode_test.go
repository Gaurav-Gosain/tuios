package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The lock glyphs the dock's mode pill shows while keybindings.lock is on,
// with Nerd Fonts (nf-fa-lock, nf-fa-unlock) and without.
const (
	lockGlyph   = ""
	unlockGlyph = ""
)

// dockLock reports the lock state the dock shows: "locked", "unlocked" or "".
func dockLock(s tuitest.Screen) string {
	text := s.Text()
	switch {
	case strings.Contains(text, unlockGlyph) || strings.Contains(text, "UNLOCK"):
		return "unlocked"
	case strings.Contains(text, lockGlyph) || strings.Contains(text, "LOCK"):
		return "locked"
	}
	return ""
}

// lockedSession is panesSession for a config with lock on. It leaves the
// client in terminal mode, where an attached client starts: alt+esc, which
// panesSession presses to reach window mode, is a key lock gives the pane.
func lockedSession(t *testing.T, name, config string) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\nlayout = \"bsp\"\n"+config)
	if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
		t.Fatalf("create %s: %v\n%s", name, err, out)
	}
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", name}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached to %s: %v\n%s", name, err, term.Snapshot())
	}
	clientShows(t, base, name)
	return term, base
}

// TestLockModeGivesEveryKeyToThePane is zellij's locked mode under the zellij
// preset, which turns keybindings.lock on with ctrl+g as the leader.
//
//  1. Locked, alt+2 (a workspace chord), alt+n (a terminal_mode key) and
//     ctrl+p (a global key) reach cat -v in the pane as ^[2^[n^P. The session
//     stays on workspace 1, focus stays on the pane, and no palette opens.
//  2. ctrl+g p h moves focus left, and the pane menu stays open: l and h
//     without the leader move focus again, and the dock says unlocked. esc
//     locks again, so the h and alt+2 after it reach cat -v as h^[2 and
//     focus does not move.
//  3. ctrl+g p d splits the pane, which is not a repeatable action, so the
//     client locks again at once: alt+2 after it goes to the pane and the
//     session stays on workspace 1.
//
// The frames "locked" and "unlocked" show the dock's mode pill in each state,
// under TUIOS_E2E_FRAMES.
//
// Negative control: on a build without lock mode the first alt+2 switches
// to workspace 2 and step 1 fails. Without the menu hold (menuKey.ran
// returning false), the l after ctrl+g p h goes to the pane and the wait for
// focus to move back right fails. Both runs are in NEGATIVE_CONTROLS.md.
func TestLockModeGivesEveryKeyToThePane(t *testing.T) {
	const sess = "lockkeys"
	term, base := lockedSession(t, sess, "\n[keybindings]\npreset = \"zellij\"\n")
	a := waitForSettledGeometryIn(t, base, sess, 1)[0].ID
	b := newWindowIn(t, base, sess)
	waitForSettledGeometryIn(t, base, sess, 2)
	if out, err := tuiosCLI(t, base, "focus-window", "-s", sess, b); err != nil {
		t.Fatalf("focus b: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return dockLock(s) == "locked" }, uiTimeout); err != nil {
		t.Fatalf("the dock does not say locked: %v\n%s", err, term.Snapshot())
	}
	railShot(t, term, "locked")

	send := func(keys ...any) {
		t.Helper()
		if err := term.SendKeys(keys...); err != nil {
			t.Fatalf("send %v: %v", keys, err)
		}
	}
	waitFocus := func(want, what string) {
		t.Helper()
		var got string
		deadline := time.Now().Add(uiTimeout)
		for time.Now().Before(deadline) {
			if got = focusedIn(t, base, sess); got == want {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("ASSERTION: %s: focus is on %s, want %s\n%s", what, got, want, term.Snapshot())
	}
	stays := func(ws int, focus, what string) {
		t.Helper()
		time.Sleep(700 * time.Millisecond)
		if got := currentWorkspace(t, base, sess); got != ws {
			t.Fatalf("ASSERTION: %s: the session is on workspace %d, want %d", what, got, ws)
		}
		if focus != "" {
			if got := focusedIn(t, base, sess); got != focus {
				t.Fatalf("ASSERTION: %s: focus moved to %s, want %s", what, got, focus)
			}
		}
	}

	send("cat -v", tuitest.Enter)
	time.Sleep(500 * time.Millisecond)

	// 1. Locked: the tuios keys are bytes for the pane.
	send(tuitest.Alt('2'), tuitest.Alt('n'), tuitest.Ctrl('p'), tuitest.Enter)
	if err := term.WaitForText("^[2^[n^P", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: alt+2 alt+n ctrl+p did not reach cat -v as bytes: %v\n%s", err, term.Snapshot())
	}
	stays(1, b, "locked alt+2 alt+n ctrl+p")
	if strings.Contains(term.Snapshot(), paletteTitle) {
		t.Fatalf("ASSERTION: locked ctrl+p opened the palette\n%s", term.Snapshot())
	}

	// 2. A focus move holds the pane menu open until esc.
	send(tuitest.Ctrl('g'), "p", "h")
	waitFocus(a, "ctrl+g p h")
	send("l")
	waitFocus(b, "l after ctrl+g p h, without the leader")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return dockLock(s) == "unlocked" }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the dock does not say unlocked while the menu is held: %v\n%s", err, term.Snapshot())
	}
	railShot(t, term, "unlocked")
	send("h")
	waitFocus(a, "h, still without the leader")
	send("l")
	waitFocus(b, "l, still without the leader")
	send(tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return dockLock(s) == "locked" }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc did not lock again: %v\n%s", err, term.Snapshot())
	}
	send("h", tuitest.Alt('2'), tuitest.Enter)
	if err := term.WaitForText("h^[2", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: h alt+2 after esc did not reach cat -v: %v\n%s", err, term.Snapshot())
	}
	stays(1, b, "h alt+2 after esc")

	// 3. One action that does not repeat, then locked again.
	send(tuitest.Ctrl('g'), "p", "d")
	waitForSettledGeometryIn(t, base, sess, 3)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return dockLock(s) == "locked" }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the client did not lock again after ctrl+g p d: %v\n%s", err, term.Snapshot())
	}
	c := focusedIn(t, base, sess)
	send(tuitest.Alt('2'))
	stays(1, c, "alt+2 after ctrl+g p d")
}
