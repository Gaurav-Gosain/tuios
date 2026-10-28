package tuie2e

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// nestConfig is the default keys: nest is the leader then enter, as
// tmux-matryoshka's prefix Enter.
const nestConfig = ""

// dockNested reports whether the dock's mode pill says nest mode is on, and
// names F12 as the key that ends it.
func dockNested(s tuitest.Screen) bool {
	return strings.Contains(s.Text(), "NEST F12")
}

// TestNestModeGivesEveryKeyToThePane is tmux-matryoshka's prefix Enter: nest
// mode gives the focused pane every key, the leader included, until the exit
// key, F12 unless keybindings.nest_exit_key says otherwise.
//
//  1. ctrl+b enter turns nest mode on, and the dock's mode pill says NEST.
//  2. ctrl+b, alt+2 (a workspace chord), ctrl+p (a global key) and x reach
//     cat -v in the pane as ^B^[2^Px. The session stays on workspace 1, no
//     palette opens and no pane is added or closed.
//  3. F12 turns it off: the pill no longer says NEST, F12 itself does not
//     reach the pane, and ctrl+b c opens a pane again.
//
// The frames "nested" and "unnested" show the dock in each state, under
// TUIOS_E2E_FRAMES.
//
// Negative control: with the nest path in HandleTerminalModeKey cut (`if
// false && o.Nested()`), the client is only locked: the ctrl+b of step 2 arms
// the leader and ^B never reaches cat -v. With the exit key check cut, F12
// goes to the pane, the pill keeps saying NEST F12, and step 3 fails. The
// runs are in NEGATIVE_CONTROLS.md.
func TestNestModeGivesEveryKeyToThePane(t *testing.T) {
	const sess = "nestkeys"
	term, base := lockedSession(t, sess, nestConfig)
	waitForSettledGeometryIn(t, base, sess, 1)
	send := func(keys ...any) {
		t.Helper()
		if err := term.SendKeys(keys...); err != nil {
			t.Fatalf("send %v: %v", keys, err)
		}
	}

	send("cat -v", tuitest.Enter)
	time.Sleep(500 * time.Millisecond)
	if dockNested(term.Screen()) {
		t.Fatalf("setup: the dock says NEST before ctrl+b enter\n%s", term.Snapshot())
	}

	// 1. On.
	send(tuitest.Ctrl('b'), tuitest.Enter)
	if err := term.WaitFor(dockNested, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: ctrl+b enter did not turn nest mode on: %v\n%s", err, term.Snapshot())
	}
	railShot(t, term, "nested")

	// 2. Every key is the pane's, the leader too.
	send(tuitest.Ctrl('b'), tuitest.Alt('2'), tuitest.Ctrl('p'), "x", tuitest.Enter)
	if err := term.WaitForText("^B^[2^Px", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: ctrl+b alt+2 ctrl+p x did not reach cat -v as bytes: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(700 * time.Millisecond)
	if got := currentWorkspace(t, base, sess); got != 1 {
		t.Fatalf("ASSERTION: alt+2 in nest mode moved the session to workspace %d", got)
	}
	if n := windowCount(t, base, sess); n != 1 {
		t.Fatalf("ASSERTION: ctrl+b x in nest mode changed the panes: %d windows, want 1", n)
	}
	if strings.Contains(term.Snapshot(), paletteTitle) {
		t.Fatalf("ASSERTION: ctrl+p in nest mode opened the palette\n%s", term.Snapshot())
	}

	// 3. Off, and the leader is tuios's again.
	send(tuitest.F12)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !dockNested(s) }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: F12 did not turn nest mode off: %v\n%s", err, term.Snapshot())
	}
	railShot(t, term, "unnested")
	send("nestoff", tuitest.Enter)
	if err := term.WaitForText("nestoff", uiTimeout); err != nil {
		t.Fatalf("setup: a word after F12 did not reach cat -v: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Snapshot(), "^[[24~") {
		t.Fatalf("ASSERTION: F12 reached the pane\n%s", term.Snapshot())
	}
	send(tuitest.Ctrl('b'), "c")
	waitForSettledGeometryIn(t, base, sess, 2)
}

// TestNestModeHandsTheLeaderToANestedTmux is the case nest mode is for: a
// tmux in the pane, with the same leader as tuios. In nest mode ctrl+b c
// opens a window in that tmux, not a pane in tuios, and ctrl+b d detaches
// it. After F12, ctrl+b c is tuios's again. Skipped without tmux.
//
// Negative control: with the nest path in HandleTerminalModeKey cut, ctrl+b
// arms the tuios leader, and the wait for tmux's second window fails. With
// the exit key check cut, the last wait for the pill to drop NEST F12 fails.
func TestNestModeHandsTheLeaderToANestedTmux(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}
	const sess = "nesttmux"
	term, base := lockedSession(t, sess, nestConfig)
	waitForSettledGeometryIn(t, base, sess, 1)
	send := func(keys ...any) {
		t.Helper()
		if err := term.SendKeys(keys...); err != nil {
			t.Fatalf("send %v: %v", keys, err)
		}
	}

	// A tmux of its own: its own server, no user config, and a status line
	// that names the windows W0, W1, so the test reads them off the screen.
	// -L, not a socket under base: a socket path has a short limit, which
	// the test's temporary folder is past on macOS.
	server := "tuios-e2e-nest-" + strconv.Itoa(os.Getpid())
	t.Cleanup(func() { _ = exec.Command(tmux, "-L", server, "kill-server").Run() })
	send(tmux+" -L "+server+" -f /dev/null new-session \\; set status-left INNER-TMUX \\; set -g window-status-format W#I \\; set -g window-status-current-format W#I", tuitest.Enter)
	if err := term.WaitForText("INNER-TMUX", uiTimeout); err != nil {
		t.Fatalf("setup: the nested tmux did not start: %v\n%s", err, term.Snapshot())
	}
	// Its status line shows before it reads keys: tmux 3.3a drops the keys
	// that come before the answers to the questions it asks the terminal at
	// start. A command it runs is the proof it reads them.
	send("echo READY-$((40+2))", tuitest.Enter)
	if err := term.WaitForText("READY-42", uiTimeout); err != nil {
		t.Fatalf("setup: the nested tmux does not run a command: %v\n%s", err, term.Snapshot())
	}

	send(tuitest.Ctrl('b'), tuitest.Enter)
	if err := term.WaitFor(dockNested, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: ctrl+b enter did not turn nest mode on: %v\n%s", err, term.Snapshot())
	}
	send(tuitest.Ctrl('b'), "c")
	if err := term.WaitForText("W1", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: ctrl+b c did not open a window in the nested tmux: %v\n%s", err, term.Snapshot())
	}
	if n := windowCount(t, base, sess); n != 1 {
		t.Fatalf("ASSERTION: ctrl+b c in nest mode opened a tuios pane: %d windows, want 1", n)
	}
	send(tuitest.Ctrl('b'), "d")
	if err := term.WaitForText("[detached", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: ctrl+b d did not detach the nested tmux: %v\n%s", err, term.Snapshot())
	}

	send(tuitest.F12)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !dockNested(s) }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: F12 did not turn nest mode off: %v\n%s", err, term.Snapshot())
	}
	send(tuitest.Ctrl('b'), "c")
	waitForSettledGeometryIn(t, base, sess, 2)
}

// TestNestModeIsTheLeaderThenEnterInEveryPreset: the default key, the leader
// then enter, is free in every preset, so it turns nest mode on under each,
// with the preset's own leader: ctrl+b for tmux, ctrl+a for screen and
// ctrl+g for zellij, whose preset also turns lock on. F12 turns it off.
//
// Negative control: with the default key removed from DefaultConfig, the
// leader then enter does nothing under every preset and the wait for NEST F12
// fails. The run is in NEGATIVE_CONTROLS.md.
func TestNestModeIsTheLeaderThenEnterInEveryPreset(t *testing.T) {
	for _, tc := range []struct {
		preset string
		leader rune
	}{
		{"tmux", 'b'},
		{"screen", 'a'},
		{"zellij", 'g'},
	} {
		t.Run(tc.preset, func(t *testing.T) {
			sess := "nest" + tc.preset
			term, base := lockedSession(t, sess, "\n[keybindings]\npreset = \""+tc.preset+"\"\n")
			waitForSettledGeometryIn(t, base, sess, 1)
			if err := term.SendKeys(tuitest.Ctrl(tc.leader), tuitest.Enter); err != nil {
				t.Fatalf("send the leader then enter: %v", err)
			}
			if err := term.WaitFor(dockNested, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: the %s leader then enter did not turn nest mode on: %v\n%s", tc.preset, err, term.Snapshot())
			}
			if err := term.SendKeys(tuitest.F12); err != nil {
				t.Fatalf("send F12: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool { return !dockNested(s) }, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: F12 did not turn nest mode off under %s: %v\n%s", tc.preset, err, term.Snapshot())
			}
		})
	}
}

// TestNestExitKeyThatCannotBeReadIsF12 is keybindings.nest_exit_key read as
// leader_key is. A key that cannot be read ("ctrl+", a modifier with no key)
// would leave nest mode with no way out, so it goes back to F12: the pill
// names F12 and F12 ends it. A key programs type ("x") is kept, and the log
// viewer says why it is a poor choice.
//
// Negative control: with the nest_exit_key block cut from DropUnreadableKeys,
// the pill names the unreadable key and F12 does not end nest mode. With the
// warning cut from ValidateConfig, the log has no line for "x". The runs are
// in NEGATIVE_CONTROLS.md.
func TestNestExitKeyThatCannotBeReadIsF12(t *testing.T) {
	t.Run("unreadable", func(t *testing.T) {
		const sess = "nestbadkey"
		term, base := lockedSession(t, sess, "\n[keybindings]\nnest_exit_key = \"ctrl+\"\n")
		waitForSettledGeometryIn(t, base, sess, 1)
		if err := term.SendKeys(tuitest.Ctrl('b'), tuitest.Enter); err != nil {
			t.Fatalf("ctrl+b enter: %v", err)
		}
		if err := term.WaitFor(dockNested, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: an unreadable nest_exit_key did not fall back to F12 on the pill: %v\n%s", err, term.Snapshot())
		}
		if err := term.SendKeys(tuitest.F12); err != nil {
			t.Fatalf("F12: %v", err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "NEST") }, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: F12 did not end nest mode with an unreadable nest_exit_key: %v\n%s", err, term.Snapshot())
		}
	})
	for _, tc := range []struct{ name, key string }{
		{"typed", "x"},
		{"ctrl-m", "ctrl+m"},
		{"the-leader", "ctrl+b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const sess = "nesttyped"
			term, base := lockedSession(t, sess, "\n[keybindings]\nnest_exit_key = \""+tc.key+"\"\n")
			waitForSettledGeometryIn(t, base, sess, 1)
			if err := term.SendKeys(tuitest.Ctrl('b'), "D", "l"); err != nil {
				t.Fatalf("open the log viewer: %v", err)
			}
			if err := term.WaitForText("copy errors", uiTimeout); err != nil {
				t.Fatalf("the log viewer did not open: %v\n%s", err, term.Snapshot())
			}
			if err := term.WaitForText("nest_exit_key", uiTimeout); err != nil {
				t.Fatalf("ASSERTION: the log has no warning for nest_exit_key = %q: %v\n%s", tc.key, err, term.Snapshot())
			}
		})
	}
}

// TestNestExitKeyFiresOnTheComposedCharacter is the exit key matched as the
// leader is. With nest_exit_key = "opt+1" on a Mac terminal where Option
// composes, Option and 1 arrives as "¡", and that has to end nest mode, as it
// starts the leader in TestOptionLeaderFiresOnTheComposedCharacter.
//
// Negative control: with isNestExitKey matching only the key produced, the
// "¡" is typed into the pane and nest mode stays on. The run is in
// NEGATIVE_CONTROLS.md.
func TestNestExitKeyFiresOnTheComposedCharacter(t *testing.T) {
	base := t.TempDir()
	const session = "e2e-nest-opt"
	term := macSession(t, base, session, "[keybindings]\nnest_exit_key = \"opt+1\"\noption_glyphs = \"bind\"\n")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ready-$((1+5))", "ready-6", shellTimeout)
	if err := term.SendKeys(tuitest.Ctrl('b'), tuitest.Enter); err != nil {
		t.Fatalf("ctrl+b enter: %v", err)
	}
	if err := term.WaitForText("NEST Opt+1", uiTimeout); err != nil {
		t.Fatalf("setup: nest mode did not turn on with its Opt+1 exit key: %v\n%s", err, term.Snapshot())
	}
	press(t, term, "Option and 1 (composed)", normalOptionInvExcl)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "NEST") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the composed character did not end nest mode: %v\n%s", err, term.Snapshot())
	}
}

// TestNestModeDoesNotComeBackAfterWindowMode: a path that sets the mode
// directly ends nest mode with it. Closing the last pane drops the client to
// window mode by assignment, and a session switch restores terminal mode the
// same way. Nest mode must not come back with it: the pill does not say NEST,
// and ctrl+b c opens a pane in the new session, as the leader is tuios's.
//
// Negative control: with settleNest not run from Update, the switch brings
// nest mode back: the pill says NEST F12 and ctrl+b c reaches the pane. The
// run is in NEGATIVE_CONTROLS.md.
func TestNestModeDoesNotComeBackAfterWindowMode(t *testing.T) {
	const sess = "nestleft"
	term, base := lockedSession(t, sess, "")
	pane := waitForSettledGeometryIn(t, base, sess, 1)[0].ID
	if out, err := tuiosCLI(t, base, "new", "nestother", "--detach"); err != nil {
		t.Fatalf("create nestother: %v\n%s", err, out)
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), tuitest.Enter); err != nil {
		t.Fatalf("ctrl+b enter: %v", err)
	}
	if err := term.WaitFor(dockNested, uiTimeout); err != nil {
		t.Fatalf("setup: ctrl+b enter did not turn nest mode on: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "close-window", "-s", sess, pane); err != nil {
		t.Fatalf("close the last pane: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !dockNested(s) }, uiTimeout); err != nil {
		t.Fatalf("setup: the pill still says NEST with no pane: %v\n%s", err, term.Snapshot())
	}
	switchClient(t, base, term, "nestother")
	waitForSettledGeometryIn(t, base, "nestother", 1)
	time.Sleep(time.Second)
	if dockNested(term.Screen()) {
		t.Fatalf("ASSERTION: nest mode came back after the session switch\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "c"); err != nil {
		t.Fatalf("ctrl+b c: %v", err)
	}
	waitForSettledGeometryIn(t, base, "nestother", 2)
}

// TestNestModeSurvivesAMouseGesture: a drag with the mouse borrows window
// mode from its press to its release, over many messages. Nest mode must wait
// for it: after an alt+drag of the nested pane, the pill still says NEST F12
// and ctrl+b still reaches cat -v.
//
// Negative control: with settleNest ending nest mode whatever borrowed window
// mode, the press ends it and ctrl+b opens the tuios leader. The run is in
// NEGATIVE_CONTROLS.md.
func TestNestModeSurvivesAMouseGesture(t *testing.T) {
	const sess = "nestdrag"
	term, base := lockedSession(t, sess, "")
	waitForSettledGeometryIn(t, base, sess, 1)
	newWindowIn(t, base, sess)
	rects := waitForSettledGeometryIn(t, base, sess, 2)
	send := func(keys ...any) {
		t.Helper()
		if err := term.SendKeys(keys...); err != nil {
			t.Fatalf("send %v: %v", keys, err)
		}
	}
	send("cat -v", tuitest.Enter)
	time.Sleep(500 * time.Millisecond)
	send(tuitest.Ctrl('b'), tuitest.Enter)
	if err := term.WaitFor(dockNested, uiTimeout); err != nil {
		t.Fatalf("setup: ctrl+b enter did not turn nest mode on: %v\n%s", err, term.Snapshot())
	}
	r := rects[1]
	col, row := r.X+r.Width/2, r.Y+r.Height/2
	mousePress(t, term, col, row, tuitest.MouseLeft, tuitest.ModAlt)
	for i := 1; i <= 4; i++ {
		mouseMotion(t, term, col+i, row, tuitest.MouseLeft, tuitest.ModAlt)
	}
	mouseRelease(t, term, col+4, row, tuitest.MouseLeft, tuitest.ModAlt)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the frame never settled after the drag: %v", err)
	}
	if !dockNested(term.Screen()) {
		t.Fatalf("ASSERTION: the drag ended nest mode\n%s", term.Snapshot())
	}
	send(tuitest.Ctrl('b'), "x", tuitest.Enter)
	if err := term.WaitForText("^Bx", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: ctrl+b x did not reach cat -v after the drag: %v\n%s", err, term.Snapshot())
	}
}

// TestNestModeWaitsBehindTheRail: the rail's list takes the keys while it has
// the keyboard, so nest mode must not claim them: the pill says SIDEBAR, not
// NEST F12. esc gives the keys back, and the pill says NEST F12 again, and F12
// then ends nest mode.
//
// Negative control: with Nested() true while the rail has the keyboard, the
// pill says NEST F12 over the rail, whose list swallows F12. The run is in
// NEGATIVE_CONTROLS.md.
func TestNestModeWaitsBehindTheRail(t *testing.T) {
	const sess = "nestrail"
	term, base := lockedSession(t, sess, "")
	waitForSettledGeometryIn(t, base, sess, 1)
	toggleSidebarViaPalette(t, term)
	if err := term.WaitForText(sidebarHeader, uiTimeout); err != nil {
		t.Fatalf("setup: no rail: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), tuitest.Enter); err != nil {
		t.Fatalf("ctrl+b enter: %v", err)
	}
	if err := term.WaitFor(dockNested, uiTimeout); err != nil {
		t.Fatalf("setup: ctrl+b enter did not turn nest mode on: %v\n%s", err, term.Snapshot())
	}
	// A blank row near the bottom of the rail.
	mouseClick(t, term, 3, 30, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "SIDEBAR") && !dockNested(s)
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: after a click on the rail the pill does not say SIDEBAR, and not NEST F12: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	if err := term.WaitFor(dockNested, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc on the rail did not give the keys back to nest mode: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.F12); err != nil {
		t.Fatalf("F12: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !dockNested(s) }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: F12 did not end nest mode after the rail: %v\n%s", err, term.Snapshot())
	}
}

// TestNestModeReachesARailSessionPane: a session docked in the rail is a pane
// like any other, and in nest mode the one with the keyboard gets every key,
// the leader too. The docked session runs cat -v. A click gives its pane the
// keyboard, ctrl+b enter turns nest mode on, and ctrl+b x must reach cat -v
// as ^Bx. F12 ends nest mode there too, and the leader is tuios's again: the
// next ctrl+b x does not reach the pane.
//
// Negative control: with the rail pane's nest path in routeKey cut, ^Bx still
// reaches the pane through the terminal handler, but F12 does not end nest
// mode from it. The run is in NEGATIVE_CONTROLS.md.
func TestNestModeReachesARailSessionPane(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "")
	for _, name := range []string{"center", "side"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("new %s: %v\n%s", name, err, out)
		}
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "side", "--literal", "echo RAIL-READY; cat -v"); err != nil {
		t.Fatalf("type into the docked session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "side", "Enter"); err != nil {
		t.Fatalf("run cat -v in the docked session: %v\n%s", err, out)
	}
	writeConfig(t, base, `[appearance.sidebar]
position = "right"
enabled = true
width = 36
[appearance.sidebar.right]
session = "side"
`)
	term := startIn(t, base, startOpts{cols: 100, rows: 35, args: []string{"attach", "center"}})
	if err := term.WaitForText("RAIL-READY", bootTimeout); err != nil {
		t.Fatalf("setup: the rail pane does not show the docked session: %v\n%s", err, term.Snapshot())
	}
	// The rail is the right 36 columns. A click in it gives its pane the
	// keyboard, and what is typed then is cat -v's.
	mouseClick(t, term, 82, 12, tuitest.MouseLeft, 0)
	sendKeys(t, term, "railkeys")
	if err := term.WaitForText("railkeys", uiTimeout); err != nil {
		t.Fatalf("setup: the click did not give the rail pane the keyboard: %v\n%s", err, term.Snapshot())
	}

	sendKeys(t, term, tuitest.Ctrl('b'), tuitest.Enter)
	if err := term.WaitFor(dockNested, uiTimeout); err != nil {
		t.Fatalf("setup: ctrl+b enter did not turn nest mode on: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Ctrl('b'), "x")
	if err := term.WaitForText("^Bx", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: in nest mode ctrl+b x did not reach the rail pane as ^Bx: %v\n%s", err, term.Snapshot())
	}

	sendKeys(t, term, tuitest.F12)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !dockNested(s) }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: F12 did not end nest mode from the rail pane: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, "after")
	if err := term.WaitForText("after", uiTimeout); err != nil {
		t.Fatalf("setup: the rail pane lost the keyboard with nest mode: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Ctrl('b'), tuitest.Esc)
	time.Sleep(700 * time.Millisecond)
	if n := strings.Count(term.Screen().Text(), "^B"); n != 1 {
		t.Fatalf("ASSERTION: after F12 the leader still reached the rail pane (%d ^B on screen)\n%s", n, term.Snapshot())
	}
}
