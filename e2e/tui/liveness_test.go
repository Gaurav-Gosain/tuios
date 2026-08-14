package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Liveness.
//
// The fuzzer proves tuios does not violate its invariants. It does not prove
// tuios does anything. Every rule in both oracles is a prohibition, and a client
// that rendered an empty screen forever and accepted every keystroke into a void
// would satisfy nearly all of them: nothing would be spliced, no pane would
// disagree with the daemon, no grid would be the wrong size, nothing would
// panic. The one existing rule with any positive content is the size check, and
// a blank screen is the right size.
//
// That is a structural gap rather than a gap in the rule set, so it cannot be
// closed by adding more prohibitions. What closes it is a small number of
// properties of the form "this eventually happens", each naming one thing tuios
// is for.
//
// They are written as ordinary tests rather than as fuzz rules on purpose. A
// liveness property needs a known starting state to be worth anything: "typing
// shows the character" is only a claim if something is focused, in a mode that
// takes typing, and not covered by an overlay, and a fuzz run is in none of
// those states in particular. Asserted mid-run they would either be gated into
// vacuity or report the fuzzer's own wandering as a failure. Asserted here, each
// one fails exactly when the thing it names has stopped working.
//
// Each is also checked on both sides where both sides have an answer, because
// "eventually shows" has two halves: the daemon has to receive it and the client
// has to draw it, and a property that only checks one of those is half a claim.

// livenessSession is a client attached to a real daemon-backed session with one
// pane, settled in window-management mode.
func livenessSession(t *testing.T, name string) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
		t.Fatalf("create session %s: %v: %s", name, err, out)
	}
	return attachIn(t, base, name, startOpts{cols: 120, rows: 40}), base
}

// firstWindow returns the session's only window, or fails.
func firstWindow(t *testing.T, base, session string) daemonWindow {
	t.Helper()
	wl, err := daemonWindows(base, session)
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	if len(wl.Windows) == 0 {
		t.Fatalf("session %s has no windows", session)
	}
	return wl.Windows[0]
}

// TestLivenessTypingReachesTheShellAndTheScreen is the most basic thing tuios
// claims: a key pressed at a focused pane in terminal mode is input to the
// program running there, and what that program prints comes back.
//
// The marker is computed by the shell rather than typed, so the assertion cannot
// be satisfied by the echo of the keystrokes: seeing it means the bytes reached
// a real process and its output came back through the daemon.
func TestLivenessTypingReachesTheShellAndTheScreen(t *testing.T) {
	term, base := livenessSession(t, "live-type")
	enterTerminalMode(t, term)

	const want = "LIVE-24-OK"
	if err := term.SendKeys("printf 'LIVE-%s-OK\\n' 24", tuitest.Enter); err != nil {
		t.Fatalf("type the command: %v", err)
	}
	if err := term.WaitForText(want, shellTimeout); err != nil {
		t.Fatalf("typing never reached the screen: %v\n%s", err, term.Snapshot())
	}

	// The daemon has to have it too. A client that painted it from its own echo
	// and never sent it would pass the check above and nothing else.
	w := firstWindow(t, base, "live-type")
	deadline := time.Now().Add(uiTimeout)
	for {
		grid, err := daemonPane(base, "live-type", w.ID)
		if err == nil && strings.Contains(strings.Join(grid, "\n"), want) {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("the daemon's own grid never held %q that the client is showing", want)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestLivenessNewPaneIncreasesTheCount says the create chord creates something,
// on both sides. Without it, a build where the chord silently did nothing would
// pass every prohibition in the fuzzer: there would simply be fewer panes to be
// wrong about.
func TestLivenessNewPaneIncreasesTheCount(t *testing.T) {
	term, base := livenessSession(t, "live-new")

	before := settledWindowCount(t, term)
	wl, err := daemonWindows(base, "live-new")
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	daemonBefore := len(wl.Windows)

	if err := term.SendKeys(tuitest.Ctrl('b'), "c"); err != nil {
		t.Fatalf("create chord: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == before+1
	}, uiTimeout); err != nil {
		t.Fatalf("the dock never counted a new pane (%d -> %d): %v\n%s",
			before, countWindows(term.Screen()), err, term.Snapshot())
	}

	deadline := time.Now().Add(uiTimeout)
	for {
		wl, err := daemonWindows(base, "live-new")
		if err == nil && len(wl.Windows) == daemonBefore+1 {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("the dock counted a new pane the daemon does not have (daemon %d, was %d)",
				len(wl.Windows), daemonBefore)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestLivenessWorkspaceSwitchChangesTheScreen says a switch switches.
//
// A pane's output is put on workspace one and the run walks to an empty
// workspace and back. The property has two halves and both are needed: the
// content has to leave, or the switch drew the old workspace, and it has to come
// back, or the switch lost it. A build that rendered nothing at all fails the
// second half, which is the class no prohibition can catch.
func TestLivenessWorkspaceSwitchChangesTheScreen(t *testing.T) {
	term, base := livenessSession(t, "live-ws")
	w := firstWindow(t, base, "live-ws")

	const want = "WS-MARK-1"
	if err := paneSend(base, "live-ws", w.ID, "printf 'WS-%s-1\\n' MARK\n"); err != nil {
		t.Fatalf("seed the pane: %v", err)
	}
	if err := term.WaitForText(want, shellTimeout); err != nil {
		t.Fatalf("the pane never showed its own output: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "w", "3"); err != nil {
		t.Fatalf("switch to workspace 3: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), want) && dockWorkspace(s) == 3
	}, uiTimeout); err != nil {
		t.Fatalf("workspace 3 still shows workspace 1's pane: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "w", "1"); err != nil {
		t.Fatalf("switch back to workspace 1: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), want) && dockWorkspace(s) == 1
	}, uiTimeout); err != nil {
		t.Fatalf("workspace 1 never came back: %v\n%s", err, term.Snapshot())
	}
}

// TestLivenessReattachRestoresWhatWasThere is the liveness half of the splice
// bug's own story. That bug was about a reattached pane showing the wrong
// content; this is the claim that a reattached pane shows content at all.
//
// The pane is given enough history to have scrolled, so what comes back is a
// rehydration rather than a screen that never changed.
func TestLivenessReattachRestoresWhatWasThere(t *testing.T) {
	term, base := livenessSession(t, "live-attach")
	w := firstWindow(t, base, "live-attach")
	tag := w.tag()

	if err := paneSend(base, "live-attach", w.ID, paneWitnessCmd(tag, 1, 150)); err != nil {
		t.Fatalf("seed the pane: %v", err)
	}
	last := fmt.Sprintf("MK%s-150", tag)
	if err := term.WaitForText(last, shellTimeout); err != nil {
		t.Fatalf("the pane never finished printing: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if _, err := term.Wait(uiTimeout); err != nil {
		t.Fatalf("the client never exited on detach: %v\n%s", err, term.Snapshot())
	}
	_ = term.Close()

	again := attachIn(t, base, "live-attach", startOpts{cols: 120, rows: 40})
	if err := again.WaitForText(last, shellTimeout); err != nil {
		t.Fatalf("the reattached client never showed the pane's last line %q: %v\n%s",
			last, err, again.Snapshot())
	}
	// And what it did show has to be a stream rather than a splice, which is the
	// prohibition and the liveness property meeting on the same screen.
	if a, b, found := spliceIn(screenLines(again.Screen())); found {
		t.Fatalf("the reattached pane shows line %d directly above line %d\n%s",
			a.seq, b.seq, again.Snapshot())
	}
}
