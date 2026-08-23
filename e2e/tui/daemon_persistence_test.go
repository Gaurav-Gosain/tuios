package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The suite already drives daemon mode through 'tuios new' and 'tuios attach',
// so subscribe/resubscribe (workspace switching) and two clients on one session
// have real coverage. These two cover what nothing here reached: a client
// leaving and coming back to the same session, and a session outliving the
// daemon that held it. Both are the paths a user exercises the moment something
// goes wrong, and both were only ever tested below the daemon boundary.

// TestDetachAndReattachKeepsLayoutAndPaneContent is the detach the docs promise
// costs nothing: the daemon holds the PTYs and keeps an emulator fed, so a new
// client asks for each window's screen and repaints it.
//
// The marker is counted rather than merely found. A reattach resubscribes every
// pane, and answering a resubscribe with the pane's whole history is exactly how
// the workspace-switch bug painted a pane's output twice; the same mistake on
// this path would show here as a second copy.
func TestDetachAndReattachKeepsLayoutAndPaneContent(t *testing.T) {
	const marker = "REATTACHMARK-3182"

	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-reattach", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}

	first := startIn(t, base, startOpts{args: []string{"attach", "e2e-reattach"}})
	if err := first.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("first client never attached: %v\n%s", err, first.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)

	// Printed by the shell, so the typed command line cannot be mistaken for the
	// output and every hit on screen is a real paint.
	if err := first.SendKeys(`printf '%s\n' "$(echo REATTACHMARK)-3182"`, tuitest.Enter); err != nil {
		t.Fatalf("write the marker: %v", err)
	}
	if err := first.WaitForText(marker, shellTimeout); err != nil {
		t.Fatalf("the marker never appeared before the detach: %v\n%s", err, first.Snapshot())
	}

	// A second window, so the reattach has a layout to bring back and not just
	// one pane's contents.
	if err := first.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("to window mode: %v", err)
	}
	if err := first.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("never entered window management mode: %v\n%s", err, first.Snapshot())
	}
	time.Sleep(insertGuard)
	newWindow(t, first)

	if err := first.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("send leader d: %v", err)
	}
	waitExit(t, first, "after leader d")

	if !sessionListed(t, base, "e2e-reattach") {
		out, _ := tuiosCLI(t, base, "ls")
		t.Fatalf("the session did not survive the detach\nls:\n%s", out)
	}

	second := startIn(t, base, startOpts{args: []string{"attach", "e2e-reattach"}})
	if err := second.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, bootTimeout); err != nil {
		t.Fatalf("the reattached client never got both windows back (count %d): %v\n%s",
			countWindows(second.Screen()), err, second.Snapshot())
	}
	if err := second.WaitForText(marker, shellTimeout); err != nil {
		t.Fatalf("pane content did not survive the detach: %v\n%s", err, second.Snapshot())
	}
	if err := second.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled after the reattach: %v", err)
	}
	if n := strings.Count(second.Screen().Text(), marker); n != 1 {
		t.Fatalf("the reattached pane shows its output %d times, want 1\n%s", n, second.Snapshot())
	}
	alive(t, second, "after reattach")
}

// TestDetachAndReattachKeepsScrollback is the reattach twin of the pane-content
// case: a client that comes back gets a new emulator per pane, so the history
// behind the screen has to be rebuilt from the daemon as well as the screen
// itself. Counted from the oldest line the scrollback viewer can reach.
func TestDetachAndReattachKeepsScrollback(t *testing.T) {
	const last = 300

	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-rescroll", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}

	first := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "e2e-rescroll"}})
	if err := first.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("first client never attached: %v\n%s", err, first.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)

	runInShell(t, first, fmt.Sprintf("for i in $(seq 1 %d); do echo \"RS-$i-END\"; done", last),
		fmt.Sprintf("RS-%d-END", last), bulkTimeout)

	if err := first.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("send leader d: %v", err)
	}
	waitExit(t, first, "after leader d")

	second := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "e2e-rescroll"}})
	if err := second.WaitForText(fmt.Sprintf("RS-%d-END", last), bootTimeout); err != nil {
		t.Fatalf("the reattached pane never showed its screen: %v\n%s", err, second.Snapshot())
	}

	if err := second.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("to window mode: %v", err)
	}
	if err := second.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("never entered window management mode: %v\n%s", err, second.Snapshot())
	}
	time.Sleep(insertGuard)

	if err := second.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("enter copy mode: %v", err)
	}
	if err := second.WaitForText("Copy mode", uiTimeout); err != nil {
		t.Fatalf("copy mode never opened: %v\n%s", err, second.Snapshot())
	}
	if err := second.SendKeys("g", "g"); err != nil {
		t.Fatalf("jump to oldest: %v", err)
	}
	if err := second.WaitForText("RS-1-END", uiTimeout); err != nil {
		t.Fatalf("the pane's scrollback did not survive the reattach: %v\n%s",
			err, second.Snapshot())
	}
	alive(t, second, "after reattach scrollback")
}

// TestSessionSurvivesTheDaemonAndSaysItWasRestored is resurrection through the
// real binary: kill the server, start a fresh one, and the session is back.
//
// It also pins what a restored session now says about itself. The shells are
// new, and before the tag the only evidence of that was a per-pane banner that
// scrolls away, so a session that had lost every process looked exactly like one
// that had been running for days.
func TestSessionSurvivesTheDaemonAndSaysItWasRestored(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-restore", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}

	first := startIn(t, base, startOpts{args: []string{"attach", "e2e-restore"}})
	if err := first.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("first client never attached: %v\n%s", err, first.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)
	if err := first.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("to window mode: %v", err)
	}
	if err := first.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("never entered window management mode: %v\n%s", err, first.Snapshot())
	}
	time.Sleep(insertGuard)
	newWindow(t, first)

	// kill-server is synchronous and saves every session on the way out.
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v: %s", err, out)
	}
	waitExit(t, first, "after kill-server")

	// Starting any session starts a daemon, and a daemon restores on start.
	if out, err := tuiosCLI(t, base, "new", "e2e-trigger", "--detach"); err != nil {
		t.Fatalf("start a fresh daemon: %v: %s", err, out)
	}

	restored := waitForSessionInfo(t, base, "e2e-restore")
	if restored.WindowCount != 2 {
		t.Errorf("the restored session has %d windows, want the 2 it had", restored.WindowCount)
	}
	if !restored.Restored {
		t.Error("the restored session is not marked restored, so nothing but a pane banner says its shells are new")
	}
	// A session nobody restored must not wear the tag.
	if trigger := waitForSessionInfo(t, base, "e2e-trigger"); trigger.Restored {
		t.Error("a freshly created session is marked restored")
	}

	// 'tuios ls' has to say it too, since that is where a user looks first.
	out, err := tuiosCLI(t, base, "ls")
	if err != nil {
		t.Fatalf("ls: %v: %s", err, out)
	}
	if !strings.Contains(out, "restored") {
		t.Errorf("'tuios ls' does not mention the restored session:\n%s", out)
	}

	// And the attach path, before it takes the screen.
	second, logPath := startInLogged(t, base, startOpts{args: []string{"attach", "e2e-restore"}})
	if err := second.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, bootTimeout); err != nil {
		t.Fatalf("the restored session did not bring its layout back (count %d): %v\n%s",
			countWindows(second.Screen()), err, second.Snapshot())
	}
	logBytes, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logBytes), "was restored") {
		t.Errorf("the attach said nothing about the session having been restored; PTY log:\n%s", string(logBytes))
	}

	// The mark is spent: attaching answered the question it exists to ask.
	if again := waitForSessionInfo(t, base, "e2e-restore"); again.Restored {
		t.Error("the restored mark survived the first attach")
	}
	alive(t, second, "after attaching to the restored session")
}

// sessionInfo is the part of 'tuios ls --json' these tests read.
type sessionInfo struct {
	Name        string `json:"name"`
	WindowCount int    `json:"window_count"`
	Restored    bool   `json:"restored"`
}

// waitForSessionInfo polls the daemon's listing until the named session appears,
// so a test never races the daemon's own start.
func waitForSessionInfo(t *testing.T, base, name string) sessionInfo {
	t.Helper()
	deadline := time.Now().Add(bootTimeout)
	var last string
	for time.Now().Before(deadline) {
		out, err := tuiosCLI(t, base, "ls", "--json")
		last = out
		if err == nil {
			var sessions []sessionInfo
			if json.Unmarshal([]byte(out), &sessions) == nil {
				for _, s := range sessions {
					if s.Name == name {
						return s
					}
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("session %q never appeared in the listing; last ls --json:\n%s", name, last)
	return sessionInfo{}
}
