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
