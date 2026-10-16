package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// `tuios daemon upgrade`: the command, what an attached client is told, and
// the links to other machines across it. See docs/design/live-handoff.md,
// section 5, PR 5, and the helpers of live_handoff_test.go and
// live_upgrade_test.go.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Live handoff").

// daemonUpgrade runs `tuios daemon upgrade --binary bin --json` and decodes
// its outcome.
func daemonUpgrade(t *testing.T, base, bin string) (map[string]any, string, error) {
	t.Helper()
	out, err := handoffCLI(t, base, 90*time.Second, "daemon", "upgrade", "--binary", bin, "--json")
	var res map[string]any
	lines := strings.Split(strings.TrimSpace(out), "\n")
	_ = json.Unmarshal([]byte(lines[len(lines)-1]), &res)
	return res, out, err
}

// childCommands is the command line of every child of pid.
func childCommands(t *testing.T, pid int) []string {
	t.Helper()
	var out []string
	if runtime.GOOS == "linux" {
		entries, _ := os.ReadDir("/proc")
		for _, e := range entries {
			child, err := strconv.Atoi(e.Name())
			if err != nil {
				continue
			}
			stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
			if err != nil {
				continue
			}
			// The command name in field 2 may hold spaces; the fields after
			// its closing parenthesis do not.
			fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
			if len(fields) < 2 || fields[1] != strconv.Itoa(pid) {
				continue
			}
			cmdline, _ := os.ReadFile("/proc/" + e.Name() + "/cmdline")
			line := strings.ReplaceAll(strings.TrimRight(string(cmdline), "\x00"), "\x00", " ")
			if fields[0] == "Z" {
				line = "<defunct> " + line
			}
			out = append(out, strconv.Itoa(child)+" "+line)
		}
		return out
	}
	raw, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,stat=,command=").Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[1] != strconv.Itoa(pid) {
			continue
		}
		cmd := strings.Join(f[3:], " ")
		if strings.HasPrefix(f[2], "Z") {
			cmd = "<defunct> " + cmd
		}
		out = append(out, f[0]+" "+cmd)
	}
	return out
}

// TestDaemonUpgradeCommandKeepsPanes runs the command a person runs, `tuios
// daemon upgrade --binary <copy>`, and checks what it says and what it did:
// the daemon kept its pid and runs the copy, every pane kept its process, and
// the command's own report names the panes and the new image.
func TestDaemonUpgradeCommandKeepsPanes(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	second := copyTuios(t)
	const sess = "e2e-command"
	newCommandWindow(t, base, sess, "a", false, "sh", "-c", "exec sleep 600")
	newCommandWindow(t, base, sess, "b", false, "sh", "-c", "exec sleep 600")
	before := panePIDs(t, base, sess)
	img := helloImage(t, base)

	// A call waiting in the daemon when it is upgraded ends with
	// daemon_upgrading, which says to call again, and not with a dropped
	// connection.
	waiting := make(chan string, 1)
	go func() {
		out, err := handoffCLI(t, base, 60*time.Second, "call", "wait-for",
			`{"session":"`+sess+`","window":"a","condition":"window-exit","timeout":45000}`)
		waiting <- out + errString(err)
	}()
	time.Sleep(500 * time.Millisecond)

	text, err := handoffCLI(t, base, 90*time.Second, "daemon", "upgrade", "--binary", second)
	if err != nil {
		t.Fatalf("ASSERTION: tuios daemon upgrade failed: %v\n%s", err, text)
	}
	var waited string
	select {
	case waited = <-waiting:
	case <-time.After(30 * time.Second):
		waited = "the waiting call had not returned 30 s after the upgrade"
	}
	after := helloImage(t, base)
	afterPIDs := panePIDs(t, base, sess)
	exe := executableOf(t, after.PID)
	res, jsonOut, jsonErr := daemonUpgrade(t, base, second)
	writeHandoffArtifact(t, map[string]any{"text": text, "json": res, "json_output": jsonOut, "json_error": errString(jsonErr), "waiting_call": waited,
		"pid_before": img.PID, "pid_after": after.PID, "executable_after": exe, "panes_before": before, "panes_after": afterPIDs})

	for _, want := range []string{"accepts this daemon's state (handoff v1)... ok", "Paused 2 panes", "pid " + strconv.Itoa(img.PID) + " kept", "Upgraded in", "2 panes and their programs kept"} {
		if !strings.Contains(text, want) {
			t.Errorf("ASSERTION: the command's report lacks %q:\n%s", want, text)
		}
	}
	if after.PID != img.PID || after.Instance == img.Instance || exe != second {
		t.Errorf("ASSERTION: the daemon is %+v running %s after the upgrade, from %+v", after, exe, img)
	}
	for id, pid := range before {
		if afterPIDs[id] != pid {
			t.Errorf("ASSERTION: window %s had pid %d, and %d after the upgrade", id, pid, afterPIDs[id])
		}
	}
	if !strings.Contains(waited, `"daemon_upgrading"`) {
		t.Errorf("ASSERTION: a wait-for in flight during the upgrade ended with %q, want daemon_upgrading", waited)
	}
	if jsonErr != nil || res["panes"] != float64(2) || res["fallback"] != false {
		t.Errorf("ASSERTION: a second upgrade with --json reported %v: %v\n%s", res, jsonErr, jsonOut)
	}
}

// TestAttachedClientIsToldToReattach attaches a real client, upgrades the
// daemon, and reads what the client printed as it exited. A client from a
// build with live upgrades is told the daemon was upgraded, and so prints
// that its programs are still running and how to attach again, with status
// 0, instead of the daemon-lost failure.
func TestAttachedClientIsToldToReattach(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	second := copyTuios(t)
	const sess = "e2e-reattach"
	newCommandWindow(t, base, sess, "anchor", false, "sh", "-c", "exec sleep 600")
	term, logPath := startInLogged(t, base, startOpts{args: []string{"attach", sess}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("never attached: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)

	text, err := handoffCLI(t, base, 90*time.Second, "daemon", "upgrade", "--binary", second)
	if err != nil {
		t.Fatalf("tuios daemon upgrade failed: %v\n%s", err, text)
	}
	code := waitExit(t, term, "after the upgrade")
	time.Sleep(200 * time.Millisecond)
	logBytes, _ := os.ReadFile(logPath)
	var said string
	for _, line := range strings.Split(string(logBytes), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "The daemon was upgraded") || strings.Contains(line, "connection to the TUIOS daemon was lost") {
			said = line
		}
	}
	writeHandoffArtifact(t, map[string]any{"upgrade": text, "client_said": said, "client_exit": code})

	if !strings.Contains(text, "1 attached client was told to attach again") {
		t.Errorf("ASSERTION: the upgrade did not count the attached client:\n%s", text)
	}
	if !strings.HasPrefix(said, "The daemon was upgraded to") ||
		!strings.Contains(said, "Your programs are still running") ||
		!strings.Contains(said, "tuios attach "+sess) {
		t.Errorf("ASSERTION: the client said %q as it exited, want that the daemon was upgraded and how to attach again\nlog tail:\n%s", said, tailMessage(string(logBytes)))
	}
	if code != 0 {
		t.Errorf("ASSERTION: the client exited %d after an upgrade, which is not a failure", code)
	}
}

// TestFederationLinkRedialsAfterUpgrade upgrades a daemon that holds a link
// to another machine (the ssh stand-in of hosts_manage_test.go). The old
// image stops its link before the exec, and the new image dials it again:
// the link is up after the upgrade, and the daemon has exactly one ssh child
// for it, none left over from the old image, alive or not.
func TestFederationLinkRedialsAfterUpgrade(t *testing.T) {
	base := t.TempDir()
	ssh := writeFakeSSH(t, base)
	env := []string{"TUIOS_SSH=" + ssh}
	startHandoffDaemon(t, base, env...)
	second := copyTuios(t)
	newCommandWindow(t, base, "e2e-fed", "anchor", false, "sh", "-c", "exec sleep 600")
	if out, err := tuiosCLIEnv(t, base, env, "hosts", "add", "build", "someone@buildbox", "--command", tuiosBin, "--connect-timeout", "5"); err != nil {
		t.Fatalf("hosts add: %v\n%s", err, out)
	}
	waitForHostListing(t, base, func(s string) bool { return strings.Contains(s, "build") && strings.Contains(s, "up") },
		"the host never came up before the upgrade")
	img := helloImage(t, base)
	links := func() (live, dead int, all []string) {
		for _, c := range childCommands(t, img.PID) {
			// The stand-in execs this test binary as the relay for the
			// link's ssh command.
			if !strings.Contains(c, testBinary(t)) {
				continue
			}
			all = append(all, c)
			if strings.Contains(c, "<defunct>") {
				dead++
			} else {
				live++
			}
		}
		return
	}
	liveBefore, _, before := links()

	text, err := handoffCLI(t, base, 90*time.Second, "daemon", "upgrade", "--binary", second)
	if err != nil {
		t.Fatalf("tuios daemon upgrade failed: %v\n%s", err, text)
	}
	listing := waitForHostListing(t, base, func(s string) bool { return strings.Contains(s, "build") && strings.Contains(s, "up") },
		"the link to the host did not come back up after the upgrade")
	time.Sleep(500 * time.Millisecond)
	liveAfter, deadAfter, after := links()
	writeHandoffArtifact(t, map[string]any{"upgrade": text, "hosts": listing, "link_children_before": before, "link_children_after": after})

	if liveBefore == 0 {
		t.Fatalf("no ssh child carried the link before the upgrade: %v\nchildren: %v", before, childCommands(t, img.PID))
	}
	if liveAfter != liveBefore || deadAfter != 0 {
		t.Errorf("ASSERTION: after the upgrade the daemon has %d live and %d defunct link children, want %d live and none defunct: %v", liveAfter, deadAfter, liveBefore, after)
	}
}
