package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestInboxSnoozeUndoAndWake drives the Inbox's lifecycle end to end against
// a real daemon: z then 2 snoozes an errored agent for an hour, the list says
// one is snoozed and S shows it under Snoozed, the command line lists it with
// --snoozed, z on it wakes it, d dismisses it and u brings it back.
//
// Negative control: with mark-attention answering "not built yet", the
// snooze fails and the item never leaves the list.
func TestInboxSnoozeUndoAndWake(t *testing.T) {
	term, base := attachClientBase(t)

	if out, err := tuiosCLI(t, base, "new", "e2e-snooze", "--detach"); err != nil {
		t.Fatalf("create the second session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-snooze", "errored",
		"--harness", "claude-code", "-m", "build failed on main"); err != nil {
		t.Fatalf("set-agent-state errored: %v\n%s", err, out)
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	waitText(t, term, "the errored item in the Inbox", "Errored 1", "build failed on main", "z snooze")

	// z opens the picker; its footer says the four lengths.
	if err := term.SendKeys("z"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the snooze picker", "1 15m", "2 1h", "3 tomorrow 9:00", "4 until it changes")
	saveFrame(t, term, "inbox-snooze-picker")
	if err := term.SendKeys("2"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the snoozed item gone from the list", "Nothing is waiting for you.", "1 snoozed. S shows it.")
	saveFrame(t, term, "inbox-snoozed")

	out, err := tuiosCLI(t, base, "list-attention")
	if err != nil || !strings.Contains(out, "Nothing is waiting for you.") {
		t.Fatalf("list-attention still lists the snoozed item: %v\n%s", err, out)
	}
	out, err = tuiosCLI(t, base, "list-attention", "--snoozed")
	if err != nil || !strings.Contains(out, "Snoozed") || !strings.Contains(out, "build failed on main") || !strings.Contains(out, "snoozed until") {
		t.Fatalf("list-attention --snoozed does not list it: %v\n%s", err, out)
	}

	// S shows it, muted, with when it wakes; z on it wakes it.
	if err := term.SendKeys("S"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the snoozed group", "Snoozed 1", "build failed on main", "z wake")
	saveFrame(t, term, "inbox-snoozed-shown")
	if err := term.SendKeys("z"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the woken item", "Errored 1", "build failed on main")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Snoozed 1") }, uiTimeout); err != nil {
		t.Fatalf("the woken item is still under Snoozed: %v\n%s", err, term.Snapshot())
	}

	// d dismisses, and the dock offers u, which brings it back.
	if err := term.SendKeys("d"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the dismiss and its undo offer", "u undoes", "Nothing is waiting for you.")
	if err := term.SendKeys("u"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the restored item", "Errored 1", "build failed on main")
	saveFrame(t, term, "inbox-restored")
	alive(t, term, "after snooze, wake, dismiss and undo")
}

// TestNextFinishedWalksUnseenTurns: two agents in other sessions finish, and
// ctrl+b O goes to the newer first, then on a repeat to the older.
func TestNextFinishedWalksUnseenTurns(t *testing.T) {
	term, base := attachClientBase(t)
	for _, name := range []string{"e2e-older", "e2e-newer"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
		for _, state := range []string{"working", "done"} {
			if out, err := tuiosCLI(t, base, "set-agent-state", "-s", name, state, "--harness", "claude-code", "-m", "turn in "+name); err != nil {
				t.Fatalf("set-agent-state %s in %s: %v\n%s", state, name, err, out)
			}
		}
		// The two turns finish in order, a moment apart.
		time.Sleep(200 * time.Millisecond)
	}
	waitText(t, term, "both finished turns announced", "e2e-newer")

	attached := func(name string) {
		t.Helper()
		waitForRows(t, base, func(rows []lsRow) bool {
			for _, r := range rows {
				if r.Name == name && r.Attached {
					return true
				}
			}
			return false
		})
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "O"); err != nil {
		t.Fatal(err)
	}
	attached("e2e-newer")
	// Again within five seconds: the next older one.
	if err := term.SendKeys(tuitest.Ctrl('b'), "O"); err != nil {
		t.Fatal(err)
	}
	attached("e2e-older")
	alive(t, term, "after walking the finished turns")
}

// waitText waits until the screen holds every one of want.
func waitText(t *testing.T, term *tuitest.Terminal, what string, want ...string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		for _, w := range want {
			if !strings.Contains(text, w) {
				return false
			}
		}
		return true
	}, uiTimeout); err != nil {
		t.Fatalf("waiting for %s: %v\n%s", what, err, term.Snapshot())
	}
}

// TestRailMarksAFinishedTurnUnread: a finished turn the person dismissed from
// the Inbox comes back as unread when u is pressed on its row in the rail's
// agents section, for every client and for the command line.
func TestRailMarksAFinishedTurnUnread(t *testing.T) {
	term, base := attachClientBase(t)
	if out, err := tuiosCLI(t, base, "new", "e2e-unread", "--detach"); err != nil {
		t.Fatalf("create the second session: %v\n%s", err, out)
	}
	for _, state := range []string{"working", "done"} {
		if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-unread", state, "--harness", "claude-code", "-m", "wrote the docs"); err != nil {
			t.Fatalf("set-agent-state %s: %v\n%s", state, err, out)
		}
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the finished turn in the Inbox", "Done 1", "wrote the docs")
	if err := term.SendKeys("d"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the dismiss", "Nothing is waiting for you.")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Nothing is waiting for you.") }, uiTimeout); err != nil {
		t.Fatalf("esc did not close the Inbox: %v\n%s", err, term.Snapshot())
	}
	// A lone esc followed at once by another key reads as alt and that key.
	time.Sleep(insertGuard)

	// The rail: ctrl+b e takes the keyboard, G goes to its last row, the
	// footer's collapse control, and two rows up over the footer's files
	// control is the agent's row, since the agents section is pinned to the
	// bottom. u marks it unread.
	if err := term.SendKeys(tuitest.Ctrl('b'), "e"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys("G", "k", "k", "u"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the dock's word", "unread", "Marked")
	deadline := time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "list-attention", "--json")
		if strings.Contains(out, `"marked_unread": true`) || strings.Contains(out, `"marked_unread":true`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the finished turn did not come back unread:\n%s\n%s", out, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	saveFrame(t, term, "rail-marked-unread")
	alive(t, term, "after marking a finished turn unread from the rail")
}

// TestAClickedFoldFoldsAgainOnPaneFocus: agent rows long at rest fold into
// one "+N at rest" line. A click on it opens them while the rail does not
// have the keyboard, so there is no letting go of the keyboard to wait for:
// they fold again on a click outside the rail.
//
// Negative control: without the refold on a click away, the rows stay open
// after the click on the pane and the last wait times out.
func TestAClickedFoldFoldsAgainOnPaneFocus(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\nagent_rest_fold = \"1s\"\n")
	killDaemon(t, base)
	for _, name := range []string{"e2e-fold", "e2e-fa", "e2e-fb", "e2e-fc"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
	}
	for _, name := range []string{"e2e-fa", "e2e-fb", "e2e-fc"} {
		if out, err := tuiosCLI(t, base, "set-agent-state", "-s", name, "idle", "--harness", "claude-code"); err != nil {
			t.Fatalf("set-agent-state idle in %s: %v\n%s", name, err, out)
		}
	}
	// Past the one second threshold before the client draws its first rail.
	time.Sleep(1500 * time.Millisecond)

	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-fold"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	waitText(t, term, "the fold line", "+3 at rest")
	saveFrame(t, term, "rail-fold-closed")

	row, col := findText(t, term, "+3 at rest")
	mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "at rest") }, uiTimeout); err != nil {
		t.Fatalf("a click on the fold did not open it: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "rail-fold-open")

	// A click in the pane, outside the rail, and the rows fold again.
	cols, rows := term.Screen().Size()
	mouseClick(t, term, cols-20, rows/2, tuitest.MouseLeft, 0)
	waitText(t, term, "the fold after a pane was focused", "+3 at rest")
	alive(t, term, "after opening and closing the fold with the mouse")
}
