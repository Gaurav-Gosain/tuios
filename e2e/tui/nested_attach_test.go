package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const nestSession = "e2e-nest"

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

func nestedSetup(t *testing.T) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	if out, err := tuiosCLI(t, base, "new", nestSession, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	outer := attachIn(t, base, nestSession, startOpts{daemonDefault: true})
	if settledWindowCount(t, outer) == 0 {
		newWindow(t, outer)
	}
	enterTerminalMode(t, outer)
	return outer, base
}

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

func tuiosProcessesUnder(outer *tuitest.Terminal, base, marker string) []int {
	var pids []int
	for _, p := range listProcesses() {
		if p.pid == outer.Pid() || !strings.HasPrefix(p.command, tuiosBin+" ") || !strings.Contains(p.command, marker) {
			continue
		}
		if !strings.Contains(p.env, "="+base+string(filepath.Separator)) {
			continue
		}
		pids = append(pids, p.pid)
	}
	return pids
}

type process struct {
	pid          int
	command, env string
}

func listProcesses() []process {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		out, err := exec.Command("ps", "-axwwE", "-o", "pid=,command=").Output()
		if err != nil {
			return nil
		}
		var ps []process
		for line := range strings.Lines(string(out)) {
			pidField, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
			if pid, err := strconv.Atoi(pidField); err == nil {
				ps = append(ps, process{pid: pid, command: rest, env: rest})
			}
		}
		return ps
	}
	var ps []process
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		cmdline, err := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		if err != nil {
			continue
		}
		env, _ := os.ReadFile("/proc/" + e.Name() + "/environ")
		ps = append(ps, process{
			pid:     pid,
			command: strings.ReplaceAll(strings.TrimRight(string(cmdline), "\x00"), "\x00", " "),
			env:     string(env),
		})
	}
	return ps
}

var nestExit = regexp.MustCompile(`NEST-EXIT-([0-9]+)`)

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
	if text := outer.Screen().Text(); strings.Contains(text, "already has a client") || strings.Contains(text, "[CLIENT]") {
		t.Errorf("the refusal came with other output\n%s", text)
	}

	if w, h := sessionSize(t, base, nestSession); w != w0 || h != h0 {
		t.Errorf("session size changed from %dx%d to %dx%d", w0, h0, w, h)
	}
	if pids := tuiosProcessesUnder(outer, base, marker); len(pids) != 0 {
		t.Errorf("a nested tuios client is still running: %v", pids)
	}
	alive(t, outer, "after the refused nested attach")
}

// TestNestedBareTuiosIsRefused is #235 as reported: a bare tuios in a pane.
func TestNestedBareTuiosIsRefused(t *testing.T) {
	requireReal(t, "asserts on OS processes (pids, signals, /proc)")
	outer, base := nestedSetup(t)
	assertRefused(t, outer, base, tuiosBin+" --no-animations", "--no-animations")
	flat := strings.Join(strings.Fields(strings.ReplaceAll(outer.Screen().Text(), "│", "")), "")
	if !strings.Contains(flat, "tuiosnewNAME") || strings.Contains(flat, "Attachingto") {
		t.Errorf("a bare tuios in a pane did not get the bare message\n%s", outer.Snapshot())
	}
}

// TestNestedAttachIsRefused is the same with the session named, and with the
// pane's environment removed, so the daemon has to place the client by its
// process and terminal alone.
func TestNestedAttachIsRefused(t *testing.T) {
	requireReal(t, "asserts on OS processes (pids, signals, /proc)")
	outer, base := nestedSetup(t)
	cmd := "env -u TUIOS_PANE_ID -u TUIOS_WINDOW_ID -u TUIOS_SESSION -u TUIOS_PANE_TOKEN " +
		tuiosBin + " attach " + nestSession + " --no-animations"
	assertRefused(t, outer, base, cmd, "--no-animations")
}

// TestNestedAttachThroughScriptIsRefused runs the client under script, which
// gives it a terminal of its own inside the pane. Its output still lands in
// the pane, so it is refused.
func TestNestedAttachThroughScriptIsRefused(t *testing.T) {
	requireReal(t, "asserts on OS processes (pids, signals, /proc)")
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script is not installed")
	}
	outer, base := nestedSetup(t)
	inner := tuiosBin + " attach " + nestSession + " --no-animations"
	cmd := "script -qfec '" + inner + "' /dev/null"
	if runtime.GOOS != "linux" {
		cmd = "script -q /dev/null " + inner
	}
	assertRefused(t, outer, base, cmd, "--no-animations")
}

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
	requireReal(t, "asserts on OS processes (pids, signals, /proc)")
	unplaced := buildUnplaced(t)
	outer, base := nestedSetup(t)
	cmd := unplaced + " -- " + tuiosBin + " attach " + nestSession + " --no-animations"
	assertRefused(t, outer, base, cmd, "--no-animations")
}

// TestAttachFromPaneToOtherSessionWorks is the tmux-like case that stays
// allowed: a pane of one session shows another session. The other session's
// size does not depend on this one's panes, so there is no loop.
func TestAttachFromPaneToOtherSessionWorks(t *testing.T) {
	requireReal(t, "asserts on OS processes (pids, signals, /proc)")
	outer, base := nestedSetup(t)
	const other = "e2e-nest-other"
	if out, err := tuiosCLI(t, base, "new", other, "--detach"); err != nil {
		t.Fatalf("create the other session: %v: %s", err, out)
	}
	w0, h0 := waitSessionSize(t, base)

	if err := outer.SendKeys(tuiosBin+" attach "+other, tuitest.Enter); err != nil {
		t.Fatalf("type the attach: %v", err)
	}
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
	if pids := tuiosProcessesUnder(outer, base, "attach "+other); len(pids) != 1 {
		t.Errorf("want one inner client, found %v", pids)
	}
	alive(t, outer, "with a client for another session in a pane")
}

// TestForcedNestedAttachSettles is tuios attach --force from the session's own
// pane: the person asked for it, so it attaches, and the floor keeps the
// session at 20x6 or more. The size is read until it stops changing.
func TestForcedNestedAttachSettles(t *testing.T) {
	requireReal(t, "asserts on OS processes (pids, signals, /proc)")
	outer, base := nestedSetup(t)
	if w, h := waitSessionSize(t, base); w < 100 || h < 30 {
		t.Fatalf("the session never reached the client's size: %dx%d", w, h)
	}
	const marker = "--no-animations"
	if err := outer.SendKeys(tuiosBin+" attach --force "+nestSession+" "+marker, tuitest.Enter); err != nil {
		t.Fatalf("type the forced attach: %v", err)
	}
	deadline := time.Now().Add(bootTimeout)
	for len(tuiosProcessesUnder(outer, base, marker)) != 1 {
		if !time.Now().Before(deadline) {
			t.Fatalf("the forced client never started\n%s", outer.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}

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
