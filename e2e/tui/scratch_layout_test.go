package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A scratch group is a small layout of its own: split it, type in each pane,
// hide and show the whole group with one key, and a second scratch entry keeps
// a layout of its own.
//
// How this could pass wrongly, written down first:
//   - The three markers could be on screen because the group never hid. The
//     hide waits for all three to leave the screen.
//   - A show could make new panes. The daemon must list exactly three panes
//     of the group after the round trip.
//   - The second entry could share the first group's layout. Each group is
//     counted by name, and the markers of the other group must be gone.

// scratchCount counts the panes of the scratch group name in list-windows.
func scratchCount(t *testing.T, base, name string) int {
	t.Helper()
	n := 0
	for _, r := range commandRows(t, base) {
		if r.Scratch && r.ScratchName == name {
			n++
		}
	}
	return n
}

// waitScratchCount waits until the group name has n panes.
func waitScratchCount(t *testing.T, term *tuitest.Terminal, base, name string, n int, what string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if scratchCount(t, base, name) == n {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s: group %s has %d panes, want %d\n%s", what, name, scratchCount(t, base, name), n, term.Snapshot())
}

// prefix presses the leader and then key.
func prefix(t *testing.T, term *tuitest.Terminal, key any) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), key); err != nil {
		t.Fatalf("send leader %v: %v", key, err)
	}
}

// allOnScreen waits until every marker is on the screen at once.
func allOnScreen(t *testing.T, term *tuitest.Terminal, what string, markers ...string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		for _, m := range markers {
			if !strings.Contains(text, m) {
				return false
			}
		}
		return true
	}, uiTimeout); err != nil {
		t.Fatalf("%s: %v not all on screen: %v\n%s", what, markers, err, term.Snapshot())
	}
}

// noneOnScreen waits until no marker is on the screen.
func noneOnScreen(t *testing.T, term *tuitest.Terminal, what string, markers ...string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		for _, m := range markers {
			if strings.Contains(text, m) {
				return false
			}
		}
		return true
	}, uiTimeout); err != nil {
		t.Fatalf("%s: %v still on screen: %v\n%s", what, markers, err, term.Snapshot())
	}
}

func TestScratchGroupLayout(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[[keybindings.command]]\nkey = \"prefix+alt+y\"\ntype = \"scratch\"\nname = \"second\"\n")
	term := startIn(t, base, startOpts{cols: 140, rows: 44, args: []string{"new", "work"}})
	waitBoot(t, term)
	newWindow(t, term)
	typeUntil(t, term, "echo BASE-$((5*5))", "BASE-25")

	// Show the built-in group and split it into three panes.
	toggleScratch(t, term)
	waitScratchCount(t, term, base, "scratch", 1, "first show")
	typeUntil(t, term, "echo AAA-$((1+1))", "AAA-2")
	prefix(t, term, "|")
	waitScratchCount(t, term, base, "scratch", 2, "first split")
	typeUntil(t, term, "echo BBB-$((2+2))", "BBB-4")
	prefix(t, term, "-")
	waitScratchCount(t, term, base, "scratch", 3, "second split")
	typeUntil(t, term, "echo CCC-$((3+3))", "CCC-6")
	allOnScreen(t, term, "three panes in the group", "AAA-2", "BBB-4", "CCC-6", "BASE-25")
	t.Logf("the group of three over the layout:\n%s", term.Snapshot())

	// Hide the whole group, and show it again: all three are intact.
	toggleScratch(t, term)
	noneOnScreen(t, term, "after the hide", "AAA-2", "BBB-4", "CCC-6")
	allOnScreen(t, term, "the layout after the hide", "BASE-25")
	t.Logf("the layout with the group hidden:\n%s", term.Snapshot())
	toggleScratch(t, term)
	allOnScreen(t, term, "the group shown again", "AAA-2", "BBB-4", "CCC-6")
	waitScratchCount(t, term, base, "scratch", 3, "after the round trip")

	// A second entry has a layout of its own. Showing it hides the first.
	pressCommand(t, term, 'y')
	waitScratchCount(t, term, base, "second", 1, "the second group")
	noneOnScreen(t, term, "the first group behind the second", "AAA-2", "BBB-4", "CCC-6")
	typeUntil(t, term, "echo DDD-$((4*4))", "DDD-16")
	prefix(t, term, "|")
	waitScratchCount(t, term, base, "second", 2, "the second group's split")
	typeUntil(t, term, "echo EEE-$((5*5+1))", "EEE-26")
	allOnScreen(t, term, "the second group", "DDD-16", "EEE-26")
	t.Logf("the second group:\n%s", term.Snapshot())

	// Back to the first: its three panes, and none of the second's.
	toggleScratch(t, term)
	allOnScreen(t, term, "the first group again", "AAA-2", "BBB-4", "CCC-6")
	noneOnScreen(t, term, "the second group behind the first", "DDD-16", "EEE-26")
	if a, b := scratchCount(t, base, "scratch"), scratchCount(t, base, "second"); a != 3 || b != 2 {
		t.Fatalf("groups have %d and %d panes, want 3 and 2", a, b)
	}

	// tuios xpanes run inside the group opens its panes in the group.
	typeUntil(t, term, tuiosBin+" xpanes --no-sync -c 'echo XP-{}-$((9*9)); exec sh' aa bb", "XP-bb-81")
	waitScratchCount(t, term, base, "scratch", 5, "xpanes inside the group")
	allOnScreen(t, term, "xpanes panes in the group", "XP-aa-81", "XP-bb-81", "AAA-2")
	t.Logf("xpanes inside the group:\n%s", term.Snapshot())
	toggleScratch(t, term)
	noneOnScreen(t, term, "the group with xpanes hidden", "XP-aa-81", "XP-bb-81")
	alive(t, term, "after two scratch groups")
}

// A scratch group survives a daemon restart hidden: its panes come back as
// fresh shells with their own saved history, in the group's layout, and the
// scratch key shows them.
func TestScratchGroupSurvivesADaemonRestart(t *testing.T) {
	const session = "e2e-scratch-restart"
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	first := startIn(t, base, startOpts{cols: 140, rows: 44, args: []string{"attach", session}})
	if err := first.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached: %v\n%s", err, first.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)

	toggleScratch(t, first)
	typeUntil(t, first, "echo PANEONE-$((1+1))", "PANEONE-2")
	prefix(t, first, "|")
	typeUntil(t, first, "echo PANETWO-$((2+3))", "PANETWO-5")
	allOnScreen(t, first, "the group before the restart", "PANEONE-2", "PANETWO-5")
	toggleScratch(t, first)
	noneOnScreen(t, first, "the group hidden before the restart", "PANEONE-2", "PANETWO-5")

	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}
	waitExit(t, first, "after kill-server")
	if out, err := tuiosCLI(t, base, "new", session+"-trigger", "--detach"); err != nil {
		t.Fatalf("start a fresh daemon: %v\n%s", err, out)
	}
	if info := waitForSessionInfo(t, base, session); !info.Restored {
		t.Fatal("the session is not marked restored")
	}

	second := startIn(t, base, startOpts{cols: 140, rows: 44, args: []string{"attach", session}})
	if err := second.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached: %v\n%s", err, second.Snapshot())
	}
	time.Sleep(time.Second)
	if strings.Contains(second.Screen().Text(), "PANEONE-2") {
		t.Fatalf("the group came back on the screen, want it hidden\n%s", second.Snapshot())
	}
	out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", session)
	if err != nil || strings.Count(out, `"scratch": true`)+strings.Count(out, `"scratch":true`) != 2 {
		t.Fatalf("list-windows after the restart = %v\n%s", err, out)
	}

	toggleScratch(t, second)
	allOnScreen(t, second, "the restored group", "PANEONE-2", "PANETWO-5", "restored from")
	typeUntil(t, second, "echo LIVE-$((6*7))", "LIVE-42")
	t.Logf("the group after the restart:\n%s", second.Snapshot())
	alive(t, second, "after the restart")
}
