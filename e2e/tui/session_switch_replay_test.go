package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// twoSessionClient creates alpha and bravo detached and attaches a client to
// alpha, settled in terminal mode with the session's one window focused.
//
// Two sessions is the minimum that makes next_session a round trip: alt+shift+n
// walks to bravo and again walks back to alpha, so a test never has to name a
// session or drive the sidebar to switch.
func twoSessionClient(t *testing.T) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)

	for _, name := range []string{"alpha", "bravo"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}

	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "alpha"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached to alpha: %v\n%s", err, term.Snapshot())
	}
	// An attached client boots straight into terminal mode, so settle it in
	// window-management mode first and enter terminal mode from there: that is
	// the transition enterTerminalMode knows how to wait on.
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window mode: %v", err)
	}
	if err := term.WaitForText("Window Management Mode", uiTimeout); err != nil {
		t.Fatalf("client never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	enterTerminalMode(t, term)
	return term
}

// switchSession walks to the next session and waits for the switch to announce
// itself. next_session is terminal-safe, so this works without leaving the mode
// the shell is typed in.
func switchSession(t *testing.T, term *tuitest.Terminal, want string) {
	t.Helper()
	if err := term.SendKeys(tuitest.Alt("N")); err != nil {
		t.Fatalf("next session: %v", err)
	}
	if err := term.WaitForText("Session: "+want, uiTimeout); err != nil {
		t.Fatalf("never landed on %s: %v\n%s", want, err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, uiTimeout); err != nil {
		t.Fatalf("%s never settled at one window: %v\n%s", want, err, term.Snapshot())
	}
	// A switch re-arms input handling the same way a mode change does, and the
	// first keystrokes after it are dropped without it.
	time.Sleep(insertGuard + 150*time.Millisecond)
}

// TestSessionSwitchKeepsPaneContent is the reported bug: a pane that printed
// something, left for another session and come back to, must still show it.
func TestSessionSwitchKeepsPaneContent(t *testing.T) {
	const marker = "SESSMARK-5120"

	term := twoSessionClient(t)
	// Printed by the shell rather than typed, so the command line itself cannot
	// be mistaken for the output and every hit is a real paint.
	runInShell(t, term, `printf '%s\n' "$(echo SESSMARK)-5120"`, marker, shellTimeout)

	switchSession(t, term, "bravo")
	switchSession(t, term, "alpha")

	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled after the switches: %v", err)
	}
	if n := strings.Count(term.Screen().Text(), marker); n != 1 {
		t.Fatalf("the pane shows its output %d times after a session round trip, want 1\n%s",
			n, term.Snapshot())
	}
	alive(t, term, "after session switching")
}

// TestSessionSwitchDoesNotRepaintThePane is the session-path twin of the
// workspace case: arriving at a session must not paint its panes' history a
// second time under the screen already restored.
func TestSessionSwitchDoesNotRepaintThePane(t *testing.T) {
	const marker = "BRAVOMARK-8802"

	term := twoSessionClient(t)
	// A switch lands on the new session already in terminal mode, so the shell
	// can be typed at directly.
	switchSession(t, term, "bravo")
	runInShell(t, term, `printf '%s\n' "$(echo BRAVOMARK)-8802"`, marker, shellTimeout)

	switchSession(t, term, "alpha")
	switchSession(t, term, "bravo")

	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled after the switches: %v", err)
	}
	if n := strings.Count(term.Screen().Text(), marker); n != 1 {
		t.Fatalf("the pane shows its output %d times after a session round trip, want 1\n%s",
			n, term.Snapshot())
	}
	alive(t, term, "after session round trip")
}

// TestSessionSwitchKeepsScrollback is the separate half: output that scrolled
// off the pane must still be reachable in the scrollback after a session round
// trip, not only the screenful that survived on screen.
func TestSessionSwitchKeepsScrollback(t *testing.T) {
	const last = 300

	term := twoSessionClient(t)
	runInShell(t, term, fmt.Sprintf("for i in $(seq 1 %d); do echo \"SS-$i-END\"; done", last),
		fmt.Sprintf("SS-%d-END", last), bulkTimeout)

	switchSession(t, term, "bravo")
	switchSession(t, term, "alpha")

	leaveTerminalMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("enter copy mode: %v", err)
	}
	if err := term.WaitForText("COPY MODE", uiTimeout); err != nil {
		t.Fatalf("copy mode never opened: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("g", "g"); err != nil {
		t.Fatalf("jump to oldest: %v", err)
	}
	if err := term.WaitForText("SS-1-END", uiTimeout); err != nil {
		t.Fatalf("the pane's scrollback did not survive a session round trip: %v\n%s",
			err, term.Snapshot())
	}
	alive(t, term, "after session switch scrollback")
}
