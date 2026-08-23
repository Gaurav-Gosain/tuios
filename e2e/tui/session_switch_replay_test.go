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
	windowManagementMode(t, term)
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

// TestSessionSwitchSwapsPanesAtomically watches every frame a switch composes.
// The pane the user is leaving must hold until the pane they are arriving at is
// ready: a frame carrying neither session's output is the blank the flash is
// made of, whatever fills it in afterwards.
func TestSessionSwitchSwapsPanesAtomically(t *testing.T) {
	const last = 300
	amark := fmt.Sprintf("ALPHA-%d-END", last)
	const bmark = "BRAVOMARK-3311"

	term := twoSessionClient(t)
	// Enough history that catching the returning client up is a real replay
	// rather than a line or two, since a pane repainting its history is exactly
	// what the flash would look like.
	runInShell(t, term, fmt.Sprintf("for i in $(seq 1 %d); do echo \"ALPHA-$i-END\"; done", last),
		amark, bulkTimeout)
	switchSession(t, term, "bravo")
	runInShell(t, term, `printf '%s\n' "$(echo BRAVOMARK)-3311"`, bmark, shellTimeout)

	if err := term.SendKeys(tuitest.Alt("N")); err != nil {
		t.Fatalf("back to alpha: %v", err)
	}

	// Sampled rather than waited on: the fault being ruled out is a frame that
	// exists only between two good ones, which any wait would step over.
	type frame struct {
		alpha, bravo bool
		text         string
		pane         string
	}
	var frames []frame
	settled := 0
	for deadline := time.Now().Add(uiTimeout); time.Now().Before(deadline) && settled < 30; {
		s := term.Screen()
		text := s.Text()
		f := frame{
			alpha: strings.Contains(text, amark),
			bravo: strings.Contains(text, bmark),
			text:  text,
			pane:  paneRegion(s),
		}
		frames = append(frames, f)
		if f.alpha {
			settled++
		} else {
			settled = 0
		}
		time.Sleep(2 * time.Millisecond)
	}
	if settled < 30 {
		t.Fatalf("never settled back on alpha across %d frames\n%s", len(frames), term.Snapshot())
	}
	// Sampling that began after the swap had already happened would assert
	// nothing, so the run only counts if it saw the pane being left.
	sawBravo := 0
	for _, f := range frames {
		if f.bravo {
			sawBravo++
		}
	}
	if sawBravo == 0 {
		t.Fatalf("sampled %d frames and none of them was still on bravo: the sampler started too late to see the swap", len(frames))
	}
	t.Logf("sampled %d frames across the swap, %d of them still on bravo", len(frames), sawBravo)

	for i, f := range frames {
		if !f.alpha && !f.bravo {
			t.Fatalf("frame %d of %d shows neither session's output: the swap left the pane blank\n%s",
				i, len(frames), f.text)
		}
	}

	// And the pane arrives finished. A pane that is painted once and then
	// painted over is the same flash seen from the other side, so every frame
	// showing alpha has to show the same alpha.
	var first string
	for _, f := range frames {
		if !f.alpha {
			continue
		}
		if first == "" {
			first = f.pane
			continue
		}
		if f.pane != first {
			t.Fatalf("the arrived pane was repainted after it was shown\nfirst:\n%s\nlater:\n%s", first, f.pane)
		}
	}
	alive(t, term, "after the atomic swap")
}

// paneRegion is the screen above the dock, which is where pane content lives.
// The dock is excluded because its notification and its countdown change on
// their own and would make every frame differ from every other.
func paneRegion(s tuitest.Screen) string {
	_, rows := s.Size()
	var b strings.Builder
	for r := range rows - 2 {
		b.WriteString(s.Line(r))
		b.WriteByte('\n')
	}
	return b.String()
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
	if err := term.WaitForText("Copy mode", uiTimeout); err != nil {
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
