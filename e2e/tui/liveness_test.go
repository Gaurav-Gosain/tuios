package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func livenessSession(t *testing.T, name string) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
		t.Fatalf("create session %s: %v: %s", name, err, out)
	}
	return attachIn(t, base, name, startOpts{cols: 120, rows: 40}), base
}

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

	if err := term.SendKeys(tuitest.Ctrl('b'), "w", "2"); err != nil {
		t.Fatalf("switch to workspace 2: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), want) && dockWorkspace(s) == 2
	}, uiTimeout); err != nil {
		t.Fatalf("workspace 2 still shows workspace 1's pane: %v\n%s", err, term.Snapshot())
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
	if a, b, found := spliceIn(screenLines(again.Screen())); found {
		t.Fatalf("the reattached pane shows line %d directly above line %d\n%s",
			a.seq, b.seq, again.Snapshot())
	}
}
