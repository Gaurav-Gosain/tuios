package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Issue #235: tuios run inside one of its own panes attached to the same
// session. The inner client's terminal is a pane of the session, so its size
// is the session's size less the chrome, and the session is the minimum over
// its clients: every resize shrank the pane, which shrank the inner client,
// which shrank the session, until the session was 1x1 and the inner client
// spun.
//
// These tests run tuios from a pane of the session it would attach to, and
// check three things: the attach is refused with a message before the TUI
// takes the terminal, the session keeps its size, and nothing is left burning
// CPU.

const nestSession = "e2e-nest"

// sessionSize reads a session's size from 'tuios ls --json'.
func sessionSize(t *testing.T, base, name string) (int, int) {
	t.Helper()
	out, err := tuiosCLI(t, base, "ls", "--json")
	if err != nil {
		t.Fatalf("ls --json: %v\n%s", err, out)
	}
	var rows []struct {
		Name   string `json:"name"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("ls --json is not JSON: %v\n%s", err, out)
	}
	for _, r := range rows {
		if r.Name == name {
			return r.Width, r.Height
		}
	}
	t.Fatalf("session %q is not listed:\n%s", name, out)
	return 0, 0
}

// nestedSetup starts a daemon session, attaches a client to it and opens a
// shell in terminal mode, ready to type a nested tuios command into.
func nestedSetup(t *testing.T) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	if out, err := tuiosCLI(t, base, "new", nestSession, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	// daemonDefault leaves TUIOS_NO_DAEMON out of the environment, so the
	// panes do not inherit it and a bare tuios in a pane is a daemon client,
	// as it is for a user.
	outer := attachIn(t, base, nestSession, startOpts{daemonDefault: true})
	if settledWindowCount(t, outer) == 0 {
		newWindow(t, outer)
	}
	enterTerminalMode(t, outer)
	return outer, base
}

// waitSessionSize waits until the session reports a size, since the daemon
// records the client a moment after its first frame.
func waitSessionSize(t *testing.T, base string) (int, int) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		w, h := sessionSize(t, base, nestSession)
		if (w >= 100 && h >= 30) || !time.Now().Before(deadline) {
			return w, h
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// tuiosProcessesUnder returns the pids of tuios client processes whose command
// line holds marker, other than the outer client, so a test can find the
// nested client it started.
func tuiosProcessesUnder(outer *tuitest.Terminal, marker string) []int {
	entries, _ := os.ReadDir("/proc")
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == outer.Pid() {
			continue
		}
		raw, err := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		if len(args) == 0 || args[0] != tuiosBin {
			continue
		}
		if strings.Contains(strings.Join(args, " "), marker) {
			pids = append(pids, pid)
		}
	}
	return pids
}

// cpuTicks returns a process's user plus system time in clock ticks.
func cpuTicks(pid int) (int, bool) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, false
	}
	s := string(raw)
	// The command name is in parentheses and may hold spaces.
	fields := strings.Fields(s[strings.LastIndexByte(s, ')')+2:])
	utime, _ := strconv.Atoi(fields[11])
	stime, _ := strconv.Atoi(fields[12])
	return utime + stime, true
}

// assertRefused runs cmd in the pane and checks the refusal, the session size
// and that no nested client is left running.
func assertRefused(t *testing.T, outer *tuitest.Terminal, base, cmd, marker string) {
	t.Helper()
	w0, h0 := waitSessionSize(t, base)
	if w0 < 100 || h0 < 30 {
		t.Fatalf("the session never reached the client's size: %dx%d", w0, h0)
	}

	runInShell(t, outer, cmd+"; echo NEST-EXIT-$?", "NEST-EXIT-", shellTimeout)
	if err := outer.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "You are inside session")
	}, uiTimeout); err != nil {
		t.Errorf("no refusal message on screen: %v\n%s", err, outer.Snapshot())
	}
	if testing.Verbose() {
		t.Logf("frame after the refusal:\n%s", outer.Screen().Text())
	}
	if strings.Contains(outer.Snapshot(), "NEST-EXIT-0") {
		t.Errorf("the nested attach exited 0, want a failure status\n%s", outer.Snapshot())
	}

	// Let any resize storm run, then look.
	time.Sleep(2 * time.Second)
	if w, h := sessionSize(t, base, nestSession); w != w0 || h != h0 {
		t.Errorf("session size changed from %dx%d to %dx%d", w0, h0, w, h)
	}
	if pids := tuiosProcessesUnder(outer, marker); len(pids) != 0 {
		t.Errorf("a nested tuios client is still running: %v", pids)
	}
	alive(t, outer, "after the refused nested attach")
}

// TestNestedBareTuiosIsRefused is #235 as reported: a bare tuios in a pane.
func TestNestedBareTuiosIsRefused(t *testing.T) {
	outer, base := nestedSetup(t)
	// The trailing flag marks this client's command line for the process scan.
	assertRefused(t, outer, base, tuiosBin+" --no-animations", "--no-animations")
	// A bare tuios asked for nothing in particular, so it is told what it can
	// do instead. The message wraps in the pane, so it is read with the
	// borders and the spaces taken out.
	flat := strings.Join(strings.Fields(strings.ReplaceAll(outer.Screen().Text(), "│", "")), "")
	if !strings.Contains(flat, "tuiosnewNAME") || strings.Contains(flat, "Attachingto") {
		t.Errorf("a bare tuios in a pane did not get the bare message\n%s", outer.Snapshot())
	}
}

// TestNestedAttachIsRefused is the same with the session named, and with the
// pane's environment removed, so the daemon has to place the client by its
// process and terminal alone.
func TestNestedAttachIsRefused(t *testing.T) {
	outer, base := nestedSetup(t)
	cmd := "env -u TUIOS_PANE_ID -u TUIOS_WINDOW_ID -u TUIOS_SESSION -u TUIOS_PANE_TOKEN " +
		tuiosBin + " attach " + nestSession
	assertRefused(t, outer, base, cmd, "attach "+nestSession)
}

// TestAttachFromPaneToOtherSessionWorks is the tmux-like case that stays
// allowed: a pane of one session shows another session. The other session's
// size does not depend on this one's panes, so there is no loop.
func TestAttachFromPaneToOtherSessionWorks(t *testing.T) {
	outer, base := nestedSetup(t)
	const other = "e2e-nest-other"
	if out, err := tuiosCLI(t, base, "new", other, "--detach"); err != nil {
		t.Fatalf("create the other session: %v: %s", err, out)
	}
	w0, h0 := waitSessionSize(t, base)

	if err := outer.SendKeys(tuiosBin+" attach "+other, tuitest.Enter); err != nil {
		t.Fatalf("type the attach: %v", err)
	}
	// The inner client draws its own dock inside the pane, so two docks show.
	if err := outer.WaitFor(func(s tuitest.Screen) bool {
		return len(dockStatus.FindAllString(s.Text(), -1)) >= 2
	}, bootTimeout); err != nil {
		t.Fatalf("the inner client never drew the other session: %v\n%s", err, outer.Snapshot())
	}
	if strings.Contains(outer.Snapshot(), "You are inside session") {
		t.Fatalf("attaching to a different session was refused\n%s", outer.Snapshot())
	}

	time.Sleep(2 * time.Second)
	if w, h := sessionSize(t, base, nestSession); w != w0 || h != h0 {
		t.Errorf("the outer session changed size from %dx%d to %dx%d", w0, h0, w, h)
	}
	pids := tuiosProcessesUnder(outer, "attach "+other)
	if len(pids) != 1 {
		t.Fatalf("want one inner client, found %v", pids)
	}
	before, _ := cpuTicks(pids[0])
	time.Sleep(2 * time.Second)
	after, ok := cpuTicks(pids[0])
	if !ok {
		t.Fatalf("the inner client exited\n%s", outer.Snapshot())
	}
	// 2 s at 100 ticks/s is 200 ticks of one full core. An idle client uses a
	// few. The loop in #235 used about 145%.
	if used := after - before; used > 60 {
		t.Errorf("the inner client used %d ticks of CPU in 2s while idle", used)
	}
	alive(t, outer, "with a client for another session in a pane")
}

// buildUnplaced builds the helper that runs a program in a pane where the
// daemon cannot place it. See testdata/unplaced.
func buildUnplaced(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "unplaced")
	build := exec.Command("go", "build", "-o", bin, "./testdata/unplaced")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build unplaced: %v\n%s", err, out)
	}
	return bin
}

// TestUnplacedNestedClientDoesNotCollapseTheSession is the guard for the
// nested client the daemon cannot find, as through ssh to the same machine.
// The attach goes through and the session shrinks, but it stops at the floor
// and settles: no 1x1, no endless resize, no CPU spin.
// assertAttachedAndSettled runs cmd in the pane, which starts a nested client
// the daemon lets through, and checks that the session shrinks no further than
// the floor, stops resizing, and that the client does not spin.
func assertAttachedAndSettled(t *testing.T, outer *tuitest.Terminal, base, cmd, marker string) {
	t.Helper()
	if w, h := waitSessionSize(t, base); w < 100 || h < 30 {
		t.Fatalf("the session never reached the client's size: %dx%d", w, h)
	}
	if err := outer.SendKeys(cmd, tuitest.Enter); err != nil {
		t.Fatalf("type the nested attach: %v", err)
	}
	deadline := time.Now().Add(bootTimeout)
	var pids []int
	for time.Now().Before(deadline) {
		if pids = tuiosProcessesUnder(outer, marker); len(pids) == 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(pids) != 1 {
		t.Fatalf("want one nested client, found %v\n%s", pids, outer.Snapshot())
	}

	// Let the shrink run its course, then check that it stopped.
	time.Sleep(6 * time.Second)
	if strings.Contains(outer.Snapshot(), "You are inside session") {
		t.Fatalf("the attach was refused\n%s", outer.Snapshot())
	}
	w1, h1 := sessionSize(t, base, nestSession)
	before, ok := cpuTicks(pids[0])
	if !ok {
		t.Fatalf("the nested client exited\n%s", outer.Snapshot())
	}
	time.Sleep(2 * time.Second)
	after, _ := cpuTicks(pids[0])
	w2, h2 := sessionSize(t, base, nestSession)

	t.Logf("settled at %dx%d; the nested client used %d CPU ticks in 2s", w1, h1, after-before)
	if w1 < 20 || h1 < 6 {
		t.Errorf("the session collapsed to %dx%d, want at least 20x6", w1, h1)
	}
	if w1 != w2 || h1 != h2 {
		t.Errorf("the session is still resizing: %dx%d, then %dx%d", w1, h1, w2, h2)
	}
	if used := after - before; used > 60 {
		t.Errorf("the nested client used %d ticks of CPU in 2s; the loop in #235 used about 145%%", used)
	}
	alive(t, outer, "with a nested client")
}

// TestUnplacedNestedClientDoesNotCollapseTheSession is the guard for the
// nested client the daemon cannot find, as through ssh to the same machine.
// The attach goes through and the session shrinks, but it stops at the floor
// and settles: no 1x1, no endless resize, no CPU spin.
func TestUnplacedNestedClientDoesNotCollapseTheSession(t *testing.T) {
	unplaced := buildUnplaced(t)
	outer, base := nestedSetup(t)
	const marker = "--no-animations"
	assertAttachedAndSettled(t, outer, base, unplaced+" -- "+tuiosBin+" attach "+nestSession+" "+marker, marker)
}

// TestForcedNestedAttachSettles is tuios attach --force from the session's own
// pane: the person asked for it, so it attaches, and the floor stops the loop.
func TestForcedNestedAttachSettles(t *testing.T) {
	outer, base := nestedSetup(t)
	const marker = "--no-animations"
	assertAttachedAndSettled(t, outer, base, tuiosBin+" attach --force "+nestSession+" "+marker, marker)
}

// TestNestedAttachOnOwnTerminalAttaches is a client in the pane that runs on a
// terminal of its own, with the pane's variables still set: setsid and script
// give it one. Its terminal is not the pane's, so it is let through, the same
// as a terminal emulator or a tmux server started from a pane.
func TestNestedAttachOnOwnTerminalAttaches(t *testing.T) {
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script is not installed")
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid is not installed")
	}
	outer, base := nestedSetup(t)
	const marker = "--no-animations"
	cmd := "setsid -f script -qfec '" + tuiosBin + " attach " + nestSession + " " + marker + "' /dev/null"
	assertAttachedAndSettled(t, outer, base, cmd, marker)
}
