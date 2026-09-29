package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
// which shrank the session, until the session was 1x1. Its output also lands
// in the pane it draws, so it redrew its own frames without end.
//
// These tests run tuios from a pane of the session it would attach to, by
// every route the daemon can see, and check that the attach is refused with a
// message, the session keeps its size, and no nested client is left running.
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

// nestExit matches the exit status the pane echoes after a nested command.
// The typed command holds "NEST-EXIT-$?", so a digit is what says it ran.
var nestExit = regexp.MustCompile(`NEST-EXIT-([0-9]+)`)

// assertRefused runs cmd in the pane and checks the refusal, the exit status,
// the session size and that no nested client is left running.
func assertRefused(t *testing.T, outer *tuitest.Terminal, base, cmd, marker string) {
	t.Helper()
	w0, h0 := waitSessionSize(t, base)
	if w0 < 100 || h0 < 30 {
		t.Fatalf("the session never reached the client's size: %dx%d", w0, h0)
	}

	if err := outer.SendKeys(cmd+"; echo NEST-EXIT-$?", tuitest.Enter); err != nil {
		t.Fatalf("type %q: %v", cmd, err)
	}
	var status string
	if err := outer.WaitFor(func(s tuitest.Screen) bool {
		m := nestExit.FindStringSubmatch(s.Text())
		if m == nil || !strings.Contains(s.Text(), "You are inside session") {
			return false
		}
		status = m[1]
		return true
	}, bootTimeout); err != nil {
		t.Fatalf("no refusal and exit status on screen: %v\n%s", err, outer.Snapshot())
	}
	if testing.Verbose() {
		t.Logf("frame after the refusal:\n%s", outer.Screen().Text())
	}
	if status == "0" {
		t.Errorf("the nested attach exited 0, want a failure status\n%s", outer.Snapshot())
	}

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
		tuiosBin + " attach " + nestSession + " --no-animations"
	assertRefused(t, outer, base, cmd, "--no-animations")
}

// TestNestedAttachThroughScriptIsRefused runs the client under script, which
// gives it a terminal of its own inside the pane. Its output still lands in
// the pane, so it is refused.
func TestNestedAttachThroughScriptIsRefused(t *testing.T) {
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script is not installed")
	}
	outer, base := nestedSetup(t)
	cmd := "script -qfec '" + tuiosBin + " attach " + nestSession + " --no-animations' /dev/null"
	assertRefused(t, outer, base, cmd, "--no-animations")
}

// buildUnplaced builds the helper that runs a program in a pane where no
// process test can place it. See testdata/unplaced.
func buildUnplaced(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "unplaced")
	build := exec.Command("go", "build", "-o", bin, "./testdata/unplaced")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build unplaced: %v\n%s", err, out)
	}
	return bin
}

// TestUnplacedNestedClientIsRefused is the nested client no process test can
// see, as through ssh to the same machine: not under the pane, on a terminal
// of its own, with no pane variables. Its probe reaches the pane through the
// relay, and it is refused.
func TestUnplacedNestedClientIsRefused(t *testing.T) {
	unplaced := buildUnplaced(t)
	outer, base := nestedSetup(t)
	cmd := unplaced + " -- " + tuiosBin + " attach " + nestSession + " --no-animations"
	assertRefused(t, outer, base, cmd, "--no-animations")
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
	if w, h := sessionSize(t, base, nestSession); w != w0 || h != h0 {
		t.Errorf("the outer session changed size from %dx%d to %dx%d", w0, h0, w, h)
	}
	if pids := tuiosProcessesUnder(outer, "attach "+other); len(pids) != 1 {
		t.Errorf("want one inner client, found %v", pids)
	}
	alive(t, outer, "with a client for another session in a pane")
}

// TestForcedNestedAttachSettles is tuios attach --force from the session's own
// pane: the person asked for it, so it attaches, and the floor keeps the
// session at 20x6 or more. The size is read until it stops changing.
func TestForcedNestedAttachSettles(t *testing.T) {
	outer, base := nestedSetup(t)
	if w, h := waitSessionSize(t, base); w < 100 || h < 30 {
		t.Fatalf("the session never reached the client's size: %dx%d", w, h)
	}
	const marker = "--no-animations"
	if err := outer.SendKeys(tuiosBin+" attach --force "+nestSession+" "+marker, tuitest.Enter); err != nil {
		t.Fatalf("type the forced attach: %v", err)
	}
	deadline := time.Now().Add(bootTimeout)
	for len(tuiosProcessesUnder(outer, marker)) != 1 {
		if !time.Now().Before(deadline) {
			t.Fatalf("the forced client never started\n%s", outer.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Settled means the same size on three reads a second apart.
	var w, h, same int
	deadline = time.Now().Add(soakTimeout)
	for same < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Second)
		nw, nh := sessionSize(t, base, nestSession)
		if nw == w && nh == h {
			same++
		} else {
			w, h, same = nw, nh, 1
		}
	}
	if same < 3 {
		t.Fatalf("the session never settled; last %dx%d", w, h)
	}
	if w < 20 || h < 6 {
		t.Errorf("the session settled at %dx%d, want at least 20x6", w, h)
	}
	if strings.Contains(outer.Snapshot(), "You are inside session") {
		t.Errorf("the forced attach was refused\n%s", outer.Snapshot())
	}
	alive(t, outer, "with a forced nested client")
}
