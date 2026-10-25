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
//     ctrl+g p h l again, then x: x is close in the pane menu, and a held
//     menu takes only repeatable keys, so x and the alt+2 after it reach
//     cat -v as x^[2, no pane closes, and the client is locked again.
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
// focus to move back right fails. Without the filter on a held menu
// (menuKey.refuses returning false), x closes pane b and step 2b fails. The
// runs are in NEGATIVE_CONTROLS.md.
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

	// 2b. A held menu takes only the repeatable keys. x is close in the pane
	// menu, which inherits the window prefix: it must close nothing and reach
	// cat -v, and the client locks again, so alt+2 after it goes there too.
	send(tuitest.Ctrl('g'), "p", "h")
	waitFocus(a, "ctrl+g p h, the second time")
	send("l")
	waitFocus(b, "l after the second ctrl+g p h")
	send("x", tuitest.Alt('2'), tuitest.Enter)
	if err := term.WaitForText("x^[2", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: x alt+2 in the held pane menu did not reach cat -v: %v\n%s", err, term.Snapshot())
	}
	stays(1, b, "x alt+2 in the held pane menu")
	if n := windowCount(t, base, sess); n != 2 {
		t.Fatalf("ASSERTION: x in the held pane menu closed a pane: %d windows, want 2\n%s", n, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return dockLock(s) == "locked" }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: x in the held pane menu did not lock again: %v\n%s", err, term.Snapshot())
	}

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

// windowCount is the number of windows the daemon reports for sess now.
func windowCount(t *testing.T, base, sess string) int {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", sess)
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	rects, ok := parseWindows(out)
	if !ok {
		t.Fatalf("list-windows: cannot parse\n%s", out)
	}
	return len(rects)
}

// TestLockModeHeldLeaderMenuTakesOnlyRepeatKeys is lock mode with the tuios
// leader, and the floating badge in place of the dock pill.
//
// ctrl+b left moves focus, and the leader menu is held so right moves it
// back. The badge says unlocked while it is held. Two seconds later, well
// past the repeat window, x would still be the leader menu's close key. A
// held menu takes only repeatable actions, so x must reach cat -v, no pane
// closes, and the badge goes away. Last, the hold key (hold_window_mode,
// left alt) is a key like any other under lock: a pane that asked for
// releases sees its press and its release. While the menu is held, the
// which-key panel lists only the repeatable keys, with esc to close.
//
// Negative control: with menuKey.refuses returning false, x closes pane b and
// the window count check fails. Without the unlock badge, the wait for the
// badge to say unlocked fails. With the full leader table in the held
// hint, the hint check fails. With ReleaseHoldKey not gated on a hold in
// progress, the pane sees the press and not the release. The runs are in
// NEGATIVE_CONTROLS.md.
func TestLockModeHeldLeaderMenuTakesOnlyRepeatKeys(t *testing.T) {
	const sess = "lockleader"
	term, base := lockedSession(t, sess, "\n[appearance]\ndock_prefix_indicator = \"badge\"\n\n[keybindings]\nlock = true\n\n"+holdModeConfig)
	a := waitForSettledGeometryIn(t, base, sess, 1)[0].ID
	b := newWindowIn(t, base, sess)
	waitForSettledGeometryIn(t, base, sess, 2)
	if out, err := tuiosCLI(t, base, "focus-window", "-s", sess, b); err != nil {
		t.Fatalf("focus b: %v\n%s", err, out)
	}
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

	send("cat -v", tuitest.Enter)
	time.Sleep(500 * time.Millisecond)

	send(tuitest.Ctrl('b'), tuitest.Left)
	waitFocus(a, "ctrl+b left")
	send(tuitest.Right)
	waitFocus(b, "right after ctrl+b left, without the leader")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "PREFIX "+unlockGlyph) || strings.Contains(s.Text(), "PREFIX UNLOCK")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the badge does not say unlocked while the leader menu is held: %v\n%s", err, term.Snapshot())
	}
	railShot(t, term, "unlocked-badge")

	// The which-key panel of the held menu lists only the keys that still
	// work, the focus moves, and says what esc and any other key do. Close
	// window (x) and Create window (c) are leader keys the held menu refuses.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "(repeat)") && strings.Contains(text, "Focus pane") &&
			strings.Contains(text, "Close and lock")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the held leader menu's hint does not list its repeat keys and esc: %v\n%s", err, term.Snapshot())
	}
	if text := term.Screen().Text(); strings.Contains(text, "Close window") || strings.Contains(text, "Create window") {
		t.Fatalf("ASSERTION: the held leader menu's hint lists keys it refuses\n%s", term.Snapshot())
	}
	railShot(t, term, "held-hint")

	time.Sleep(2 * time.Second)
	send("x", tuitest.Alt('2'), tuitest.Enter)
	if err := term.WaitForText("x^[2", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: x alt+2 in the held leader menu did not reach cat -v: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(700 * time.Millisecond)
	if n := windowCount(t, base, sess); n != 2 {
		t.Fatalf("ASSERTION: x in the held leader menu closed a pane: %d windows, want 2\n%s", n, term.Snapshot())
	}
	if got := focusedIn(t, base, sess); got != b {
		t.Fatalf("ASSERTION: focus moved to %s, want %s", got, b)
	}
	if got := currentWorkspace(t, base, sess); got != 1 {
		t.Fatalf("ASSERTION: the session is on workspace %d, want 1", got)
	}
	if strings.Contains(term.Snapshot(), "PREFIX") {
		t.Fatalf("ASSERTION: the badge still shows after x\n%s", term.Snapshot())
	}

	// The hold key is a key lock gives the pane, its release included. A
	// pane that asked for every key and for releases (CSI > 11 u) must see
	// the press and the release of left alt, then z.
	send(tuitest.Ctrl('c'))
	time.Sleep(300 * time.Millisecond)
	send("clear; stty raw -echo; printf '\\033[>11u'; cat -v", tuitest.Enter)
	time.Sleep(time.Second)
	send(tuitest.Key(kittyLeftAltPress), tuitest.Key(kittyLeftAltRelease), "z")
	if err := term.WaitForText("^[[57443;3u^[[57443;3:3u^[[122u", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the hold key's press and release did not both reach the pane: %v\n%s", err, term.Snapshot())
	}
}

// TestLockModeGivesTheRailPaneItsWorkspaceChords is lock mode with a rail
// session pane focused. alt+1 and alt+3 are workspace chords, which lock gives
// the pane the user types into: the rail's pane, not the focused pane of the
// attached session.
//
// Then ctrl+b left holds the leader menu, and x y Enter must reach the rail
// pane as xy: x is a key the held menu refuses, and it goes to the pane the
// user types into.
//
// Negative control: with the rail's workspace chord exception kept under lock,
// alt+1 alt+3 go to the attached session's pane and the wait for them in the
// rail session fails. With menuKey.unbound sending to the focused pane of the
// attached session, x goes there and the wait for xy fails. The runs are in
// NEGATIVE_CONTROLS.md.
func TestLockModeGivesTheRailPaneItsWorkspaceChords(t *testing.T) {
	const cfg = `[startup]
open_default_window = true
[appearance.sidebar]
position = "left"
enabled = true
width = 38
sections = "sessions"
[appearance.sidebar.left]
session = "lockrail-side"
[keybindings]
lock = true
`
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, cfg)
	for _, name := range []string{"lockrail", "lockrail-side"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("new %s: %v\n%s", name, err, out)
		}
	}
	for _, sess := range []string{"lockrail", "lockrail-side"} {
		for _, keys := range [][]string{{"--literal", "cat -v"}, {"Enter"}} {
			if out, err := tuiosCLI(t, base, append([]string{"send-keys", "-s", sess}, keys...)...); err != nil {
				t.Fatalf("send to %s: %v\n%s", sess, err, out)
			}
		}
	}
	term := startIn(t, base, startOpts{cols: 138, rows: 40, args: []string{"attach", "lockrail"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(time.Second)
	// A click on the rail's pane gives it the keyboard.
	mousePress(t, term, 15, 5, tuitest.MouseLeft, 0)
	mouseRelease(t, term, 15, 5, tuitest.MouseLeft, 0)
	time.Sleep(500 * time.Millisecond)
	if err := term.SendKeys("q", tuitest.Alt('1'), tuitest.Alt('3'), tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		out, err := tuiosCLI(t, base, "capture-pane", "-s", "lockrail-side")
		if err == nil && strings.Contains(out, "q^[1^[3") {
			break
		}
		if time.Now().After(deadline) {
			center, _ := tuiosCLI(t, base, "capture-pane", "-s", "lockrail")
			t.Fatalf("ASSERTION: q alt+1 alt+3 did not reach the rail pane: %v\nrail:\n%s\ncenter:\n%s\n%s", err, out, center, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if out, err := tuiosCLI(t, base, "capture-pane", "-s", "lockrail"); err != nil || strings.Contains(out, "^[1") {
		t.Fatalf("ASSERTION: the workspace chords reached the attached session's pane: %v\n%s", err, out)
	}
	if got := currentWorkspace(t, base, "lockrail"); got != 1 {
		t.Fatalf("ASSERTION: the session is on workspace %d, want 1", got)
	}

	// A key a held menu refuses goes to the rail pane too. ctrl+b left holds
	// the leader menu, and x is a key it refuses: x and the y after it must
	// both reach the rail pane's cat -v as xy, and nothing the center.
	if err := term.SendKeys(tuitest.Ctrl('b'), tuitest.Left); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := term.SendKeys("x", "y", tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(uiTimeout)
	for {
		out, err := tuiosCLI(t, base, "capture-pane", "-s", "lockrail-side")
		if err == nil && strings.Contains(out, "xy") {
			break
		}
		if time.Now().After(deadline) {
			center, _ := tuiosCLI(t, base, "capture-pane", "-s", "lockrail")
			t.Fatalf("ASSERTION: x y after ctrl+b left did not reach the rail pane as xy: %v\nrail:\n%s\ncenter:\n%s\n%s", err, out, center, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if out, err := tuiosCLI(t, base, "capture-pane", "-s", "lockrail"); err != nil || strings.Contains(out, "x") {
		t.Fatalf("ASSERTION: the key the held menu refused reached the attached session's pane: %v\n%s", err, out)
	}
	railShot(t, term, "locked-rail")
}
