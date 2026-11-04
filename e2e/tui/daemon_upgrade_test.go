package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
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

	for _, want := range []string{"accepts this daemon's state (handoff v" + strconv.Itoa(session.HandoffVersion) + ")... ok", "Paused 2 panes", "pid " + strconv.Itoa(img.PID) + " kept", "Upgraded in", "2 panes and their programs kept"} {
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

// TestAttachedClientFollowsDaemonUpgrade attaches a real client to a session
// with a shell, upgrades the daemon to a copy of the same build, and uses the
// client afterwards. The client does not exit: it waits for the new image as
// after a restart, attaches again to the same session, says it reattached, and
// draws the pane as it was. A line typed at the client then reaches the shell,
// whose pid the upgrade kept.
func TestAttachedClientFollowsDaemonUpgrade(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	second := copyTuios(t)
	const sess = "e2e-follow"
	win := newCommandWindow(t, base, sess, "shell", false, "sh")
	term, logPath := startInLogged(t, base, startOpts{args: []string{"attach", sess}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	enterTerminalMode(t, term)
	if err := term.SendKeys("echo before-$((20+1))", tuitest.Enter); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := term.WaitForText("before-21", uiTimeout); err != nil {
		t.Fatalf("the shell never answered before the upgrade: %v\n%s", err, term.Snapshot())
	}
	pids := panePIDs(t, base, sess)
	img := helloImage(t, base)

	text, err := handoffCLI(t, base, 90*time.Second, "daemon", "upgrade", "--binary", second)
	if err != nil {
		t.Fatalf("tuios daemon upgrade failed: %v\n%s", err, text)
	}
	followed := term.WaitForText("The daemon was upgraded. tuios reattached.", bootTimeout)
	code, exited := term.ExitCode()
	afterFollow := term.Snapshot()
	var typed string
	var typedErr error
	if followed == nil && !exited {
		windowManagementMode(t, term)
		enterTerminalMode(t, term)
		if typedErr = term.SendKeys("echo after-$((40+2))", tuitest.Enter); typedErr == nil {
			typedErr = term.WaitForText("after-42", uiTimeout)
		}
		typed = paneScreen(t, base, sess, win)
	}
	after := helloImage(t, base)
	afterPIDs := panePIDs(t, base, sess)
	logBytes, _ := os.ReadFile(logPath)
	writeHandoffArtifact(t, map[string]any{"upgrade": text, "image_before": img, "image_after": after,
		"panes_before": pids, "panes_after": afterPIDs, "client_exited": exited, "client_exit": code,
		"screen_after_follow": afterFollow, "pane_after_typing": typed, "typing_error": errString(typedErr)})

	if !strings.Contains(text, "1 attached client was told to attach again") {
		t.Errorf("ASSERTION: the upgrade did not count the attached client:\n%s", text)
	}
	if after.Instance == img.Instance {
		t.Errorf("ASSERTION: the daemon's instance did not change across the upgrade: %+v", after)
	}
	if exited {
		t.Fatalf("ASSERTION: the client exited (code %d) after the upgrade instead of following the daemon\nlog tail:\n%s\n%s", code, tailMessage(string(logBytes)), afterFollow)
	}
	if followed != nil {
		t.Fatalf("ASSERTION: the client never said it reattached after the upgrade: %v\n%s", followed, afterFollow)
	}
	if !strings.Contains(afterFollow, "before-21") {
		t.Errorf("ASSERTION: the pane's screen was not drawn again after the client followed the upgrade:\n%s", afterFollow)
	}
	if typedErr != nil || !strings.Contains(typed, "after-42") {
		t.Errorf("ASSERTION: a line typed at the client after the upgrade did not reach the shell: %v\npane:\n%s\nclient:\n%s", typedErr, typed, term.Snapshot())
	}
	if afterPIDs[win] == 0 || afterPIDs[win] != pids[win] {
		t.Errorf("ASSERTION: the shell had pid %d and %d after the upgrade", pids[win], afterPIDs[win])
	}
}

// TestAttachedClientIsToldToReattach attaches a real client and upgrades the
// daemon to a build that speaks another wire protocol, one this client cannot
// follow. The client finds that in the new daemon's answer to its hello, before
// it attaches, and exits at once, well within the wait for a new daemon,
// saying that the daemon is being upgraded, that its programs are still
// running and how to attach again, with status 0 and not the daemon-lost
// failure.
func TestAttachedClientIsToldToReattach(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	other := otherProtocolTuios(t)
	const sess = "e2e-reattach"
	newCommandWindow(t, base, sess, "anchor", false, "sh", "-c", "exec sleep 600")
	term, logPath := startInLogged(t, base, startOpts{args: []string{"attach", sess}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("never attached: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)

	text, err := handoffCLI(t, base, 90*time.Second, "daemon", "upgrade", "--binary", other)
	if err != nil {
		t.Fatalf("tuios daemon upgrade failed: %v\n%s", err, text)
	}
	upgraded := time.Now()
	code, exited := -1, false
	for deadline := upgraded.Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if code, exited = term.ExitCode(); exited {
			break
		}
	}
	took := time.Since(upgraded)
	time.Sleep(200 * time.Millisecond)
	logBytes, _ := os.ReadFile(logPath)
	var said string
	for line := range strings.SplitSeq(string(logBytes), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "The daemon is being upgraded") || strings.Contains(line, "connection to the TUIOS daemon was lost") {
			said = line
		}
	}
	writeHandoffArtifact(t, map[string]any{"upgrade": text, "client_said": said, "client_exited": exited, "client_exit": code, "seconds_to_exit": took.Seconds()})

	if !strings.Contains(text, "1 attached client was told to attach again") {
		t.Errorf("ASSERTION: the upgrade did not count the attached client:\n%s", text)
	}
	if !exited {
		t.Fatalf("ASSERTION: the client still runs %s after an upgrade to a protocol it does not speak\n%s", took.Round(time.Second), term.Snapshot())
	}
	// The wait for a new daemon lasts 15 s. A client that found the protocol
	// apart exits on its first dial.
	if took > 8*time.Second {
		t.Errorf("ASSERTION: the client took %s to exit after the upgrade: it waited for a daemon it cannot talk to instead of stopping at its answer", took.Round(time.Millisecond))
	}
	if !strings.HasPrefix(said, "The daemon is being upgraded to") ||
		!strings.Contains(said, "Your programs are still running") ||
		!strings.Contains(said, "tuios attach "+sess) {
		t.Errorf("ASSERTION: the client said %q as it exited, want that the daemon was upgraded and how to attach again\nlog tail:\n%s", said, tailMessage(string(logBytes)))
	}
	if code != 0 {
		t.Errorf("ASSERTION: the client exited %d after an upgrade, which is not a failure", code)
	}
}

var (
	otherProtocolOnce sync.Once
	otherProtocolBin  string
	otherProtocolErr  error
	// otherProtocolDir holds that build, and runE2E removes it when the run
	// ends: the binary is tens of megabytes, and a test's cleanup is too early
	// for it, since every test that wants it shares the one build.
	otherProtocolDir string
)

// otherProtocolTuios builds this tree once per run with the wire protocol one
// ahead and the oldest it serves moved up with it, as a release that changes
// a message's shape would. Its daemon refuses this build's clients at the
// hello, and a client of this build would refuse its welcome.
func otherProtocolTuios(t *testing.T) string {
	t.Helper()
	otherProtocolOnce.Do(func() {
		otherProtocolBin, otherProtocolErr = buildOtherProtocol()
	})
	if otherProtocolErr != nil {
		t.Fatalf("build tuios with another wire protocol: %v", otherProtocolErr)
	}
	return otherProtocolBin
}

func buildOtherProtocol() (string, error) {
	src, err := filepath.Abs("../../internal/session/protocol_version.go")
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	next := strconv.Itoa(session.ProtocolVersion + 1)
	text := string(data)
	for _, name := range []string{"ProtocolVersion", "MinProtocolVersion"} {
		re := regexp.MustCompile(`(?m)^const ` + name + ` = \d+$`)
		if !re.MatchString(text) {
			return "", fmt.Errorf("%s does not declare %s as a number", src, name)
		}
		text = re.ReplaceAllString(text, "const "+name+" = "+next)
	}
	dir, err := os.MkdirTemp("", "tuios-e2e-other-protocol")
	if err != nil {
		return "", err
	}
	otherProtocolDir = dir
	patched := filepath.Join(dir, "protocol_version.go")
	if err := os.WriteFile(patched, []byte(text), 0o600); err != nil {
		return "", err
	}
	overlay, err := json.Marshal(map[string]any{"Replace": map[string]string{src: patched}})
	if err != nil {
		return "", err
	}
	overlayPath := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0o600); err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "tuios")
	build := exec.Command("go", "build", "-overlay", overlayPath, "-o", bin, "./cmd/tuios")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%v: %s", err, out)
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}
	return bin, nil
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

// TestDaemonUpgradeRefusalNamesTheServer refuses an upgrade of a named server
// and reads the command the refusal tells the reader to run. On a named server
// it has to carry --server, because a bare `tuios restart-server` restarts
// the default daemon and stops every program running in it.
func TestDaemonUpgradeRefusalNamesTheServer(t *testing.T) {
	base := t.TempDir()
	const server = "e2e-upname"
	if out, err := handoffCLI(t, base, 30*time.Second, "--server", server, "start-server"); err != nil {
		t.Fatalf("start-server: %v\n%s", err, out)
	}
	t.Cleanup(func() { _, _ = handoffCLI(t, base, 30*time.Second, "--server", server, "kill-server") })
	notTuios, err := exec.LookPath("false")
	if err != nil {
		t.Fatalf("find false: %v", err)
	}

	text, err := handoffCLI(t, base, 60*time.Second, "--server", server, "daemon", "upgrade", "--binary", notTuios)
	writeHandoffArtifact(t, map[string]any{"server": server, "text": text, "error": errString(err)})

	if err == nil {
		t.Fatalf("ASSERTION: an upgrade to %s was not refused:\n%s", notTuios, text)
	}
	if want := "'tuios --server " + server + " restart-server'"; !strings.Contains(text, want) {
		t.Errorf("ASSERTION: the refusal on server %s does not say to run %s:\n%s", server, want, text)
	}
	if strings.Contains(text, "'tuios restart-server'") {
		t.Errorf("ASSERTION: the refusal on server %s says to run the bare 'tuios restart-server', which restarts the default daemon:\n%s", server, text)
	}
}

// TestDaemonUpgradeCommandChecksTheBinaryFirst: `tuios daemon upgrade` runs
// the binary it is given before it asks the daemon, so it makes the daemon's
// owner and mode check first. A script anyone can write must be refused with
// the daemon's message and never run: its marker must not exist. The positive
// half: the same script, writable only by its owner, is run (its marker
// exists) and refused as no tuios.
func TestDaemonUpgradeCommandChecksTheBinaryFirst(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	bin := filepath.Join(t.TempDir(), "tuios")
	marks := t.TempDir()
	run := func(marker string, mode os.FileMode) (string, bool, error) {
		if err := os.WriteFile(bin, []byte("#!/bin/sh\necho ran >>'"+marker+"'\necho hello\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(bin, mode); err != nil {
			t.Fatal(err)
		}
		out, err := handoffCLI(t, base, 60*time.Second, "daemon", "upgrade", "--binary", bin)
		_, statErr := os.Stat(marker)
		return out, statErr == nil, err
	}
	outA, ranA, errA := run(filepath.Join(marks, "a"), 0o777)
	outB, ranB, errB := run(filepath.Join(marks, "b"), 0o755)
	writeHandoffArtifact(t, map[string]any{
		"binary": bin, "writable_by_others": map[string]any{"text": outA, "error": errString(errA), "marker_exists": ranA},
		"owner_only": map[string]any{"text": outB, "error": errString(errB), "marker_exists": ranB},
	})

	const want = "can be written by its group or by others (mode 0777), so this daemon will not run it"
	if errA == nil || !strings.Contains(outA, want) {
		t.Errorf("ASSERTION: the upgrade to a script anyone can write was not refused with %q (error %v):\n%s", want, errA, outA)
	}
	if ranA {
		t.Errorf("ASSERTION: the command ran the script anyone can write before refusing it")
	}
	if errB == nil || !strings.Contains(outB, "cannot take the daemon over") {
		t.Errorf("ASSERTION: the upgrade to the owner-only script was not refused with \"cannot take the daemon over\" (error %v):\n%s", errB, outB)
	}
	if !ranB {
		t.Errorf("ASSERTION: the command did not run the owner-only script, so the first half never reached the run")
	}
}

// TestDaemonUpgradeFromAPaneIsRefused runs `tuios daemon upgrade` from a pane
// under the default open mode, where every pane holds admin, with no client
// attached. The binary named would run as the daemon, which holds every pane's
// terminal and the key that signs every pane's token, so a pane's upgrade
// needs the person to say yes at an attached client (upgrade_confirm_test.go),
// and with none attached it is refused at once, whatever the pane's grants.
// The daemon's image must not change, and the same upgrade from outside every
// pane must go through.
func TestDaemonUpgradeFromAPaneIsRefused(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const sess = "e2e-upgrade-from-pane"
	win := newCommandWindow(t, base, sess, "agent", false, "sh")
	img := helloImage(t, base)
	second := copyTuios(t)
	started := time.Now()
	_ = mustCall(t, base, "send-text", map[string]any{"session": sess, "window": win,
		"text": tuiosBin + " daemon upgrade --binary " + second + "; echo UP_EXIT=$?\n"})
	m := waitHandoffPane(t, base, sess, win, regexp.MustCompile(`UP_EXIT=(\d+)`), 90*time.Second)
	took := time.Since(started)
	lines := mustScrollback(t, base, sess, win)
	after := helloImage(t, base)
	res, out, err := daemonUpgrade(t, base, second)
	writeHandoffArtifact(t, map[string]any{"exit": m[1], "seconds": took.Seconds(), "screen": lines, "before": img, "after": after,
		"from_outside": res, "from_outside_err": errString(err)})
	if took > 30*time.Second {
		t.Errorf("ASSERTION: with no client attached the pane's upgrade took %s to be refused, as if it waited for an answer", took)
	}
	if after.Instance != img.Instance || after.PID != img.PID {
		t.Fatalf("ASSERTION: a pane upgraded the daemon (%+v then %+v)", img, after)
	}
	if m[1] == "0" || !screenLinesHave(lines, "daemon-upgrade is for the person") {
		t.Errorf("ASSERTION: the pane's daemon upgrade was not refused as one for the person (exit %s):\n%s", m[1], strings.Join(lines, "\n"))
	}
	if err != nil {
		t.Errorf("ASSERTION: the same upgrade from outside every pane failed: %v\n%s", err, out)
	}
}

// linkRef is a pane of an agent's parent chain, as list-agents gives it.
type linkRef struct {
	Session        string `json:"session"`
	Window         string `json:"window"`
	AgentSessionID string `json:"agent_session_id"`
	Gone           bool   `json:"gone"`
}

// linkRow is the part of a list-agents row the parent link test reads.
type linkRow struct {
	Window string   `json:"window_id"`
	Name   string   `json:"name"`
	Parent *linkRef `json:"parent"`
	Root   *linkRef `json:"root"`
	Depth  int      `json:"depth"`
}

// linkRows lists the windows of session, agent or not, by name.
func linkRows(t *testing.T, base string, params map[string]any) map[string]linkRow {
	t.Helper()
	res := mustCall(t, base, "list-agents", params)
	raw, _ := json.Marshal(res["agents"])
	var rows []linkRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("list-agents rows: %v: %s", err, raw)
	}
	out := make(map[string]linkRow, len(rows))
	for _, r := range rows {
		out[r.Name] = r
	}
	return out
}

// writeChainAgent writes the fake agent of the parent link tests under base
// and returns its path. Run as `agent.sh N`, it reports idle with the
// conversation id conv-N, so it is an agent start-agent can wait for, then
// starts generation N-1 with tuios start-agent, named genN-1, from its pane.
func writeChainAgent(t *testing.T, base string) string {
	t.Helper()
	agent := filepath.Join(base, "agent.sh")
	script := "#!/bin/sh\nT='" + tuiosBin + "'\n" +
		`"$T" set-agent-state idle --agent-session-id "conv-$1"` + "\n" +
		`if [ "$1" -gt 0 ]; then "$T" start-agent "$0 $(($1 - 1))" --name "gen$(($1 - 1))" --json > "$0.start$1" 2>&1; fi` + "\n" +
		"exec sleep 600\n"
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return agent
}

// TestAgentParentLinkSurvivesUpgradeAndRestart starts a chain of agents the
// way agents start them: a fake agent in a pane reports itself idle and runs
// tuios start-agent, whose agent does the same once more. Each child must
// name its parent and the first agent as its root, the selector must find a
// parent's children and a root's whole chain, and a script outside every pane
// must link with --parent. A chain stops at 8. The links must hold across a
// live upgrade, a closed parent must read gone, and the links must hold
// across a restart that restores the session.
func TestAgentParentLinkSurvivesUpgradeAndRestart(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const sess = "e2e-link"
	agent := writeChainAgent(t, base)
	newCommandWindow(t, base, sess, "gen2", false, "sh", agent, "2")
	var rows map[string]linkRow
	deadline := time.Now().Add(60 * time.Second)
	for {
		rows = linkRows(t, base, map[string]any{"session": sess})
		if _, ok := rows["gen0"]; ok {
			break
		}
		if time.Now().After(deadline) {
			first, _ := os.ReadFile(agent + ".start2")
			second, _ := os.ReadFile(agent + ".start1")
			t.Fatalf("ASSERTION: the chain never reached gen0: rows %+v\nstart from gen2: %s\nstart from gen1: %s", rows, first, second)
		}
		time.Sleep(200 * time.Millisecond)
	}
	gen2, gen1 := rows["gen2"].Window, rows["gen1"].Window
	evidence := map[string]any{"started": rows}

	// check holds the chain to what it must be. gone is the set of panes
	// that have closed.
	check := func(when string, rows map[string]linkRow, gone map[string]bool) {
		t.Helper()
		if r := rows["gen2"]; r.Window != "" && (r.Parent != nil || r.Root != nil) {
			t.Errorf("ASSERTION %s: the root gen2 has parent %+v and root %+v, want none", when, r.Parent, r.Root)
		}
		want := map[string]struct {
			parent, conv string
			depth        int
		}{"gen1": {gen2, "conv-2", 1}, "gen0": {gen1, "conv-1", 2}}
		for name, w := range want {
			r, ok := rows[name]
			if !ok && name == "gen1" && gone[gen1] {
				continue
			}
			if r.Parent == nil || r.Root == nil {
				t.Errorf("ASSERTION %s: %s has parent %+v and root %+v", when, name, r.Parent, r.Root)
				continue
			}
			if r.Parent.Window != w.parent || r.Parent.Session != sess || r.Parent.AgentSessionID != w.conv || r.Parent.Gone != gone[w.parent] {
				t.Errorf("ASSERTION %s: %s's parent is %+v, want %s/%s with conversation %s, gone %v", when, name, *r.Parent, sess, w.parent, w.conv, gone[w.parent])
			}
			if r.Root.Window != gen2 || r.Root.Gone != gone[gen2] || r.Depth != w.depth {
				t.Errorf("ASSERTION %s: %s's root is %+v at depth %d, want %s at depth %d", when, name, *r.Root, r.Depth, gen2, w.depth)
			}
		}
	}
	check("after start", rows, nil)

	// The selector: a parent's children, and a root's whole chain.
	names := func(sel string) []string {
		t.Helper()
		var out []string
		for name := range linkRows(t, base, map[string]any{"select": sel}) {
			out = append(out, name)
		}
		slices.Sort(out)
		return out
	}
	byParent, byRoot := names("parent:"+gen2[:8]), names("root:"+gen2)
	evidence["select_parent"], evidence["select_root"] = byParent, byRoot
	if !slices.Equal(byParent, []string{"gen1"}) || !slices.Equal(byRoot, []string{"gen0", "gen1"}) {
		t.Errorf("ASSERTION: parent:%s selects %v and root:%s selects %v, want [gen1] and [gen0 gen1]", gen2[:8], byParent, gen2, byRoot)
	}
	if out, err := handoffCLI(t, base, 30*time.Second, "wait-for", "agent-state", "--select", "root:"+gen2, "--until", "idle", "--every", "--timeout", "10000"); err != nil {
		t.Errorf("ASSERTION: wait-for agent-state --select root:%s --every did not end on the idle chain: %v\n%s", gen2, err, out)
	}

	// A script outside every pane names the parent. Six more links reach
	// depth 8, and the ninth is refused with nothing started.
	parent := gen1
	for i := range 6 {
		name := "deep" + strconv.Itoa(i)
		out, err := handoffCLI(t, base, 60*time.Second, "start-agent", "-s", sess, agent+" 0", "--name", name, "--parent", parent[:12], "--json")
		if err != nil {
			t.Fatalf("ASSERTION: start-agent --parent %s: %v\n%s", parent[:12], err, out)
		}
		parent = linkRows(t, base, map[string]any{"session": sess})[name].Window
	}
	deep := linkRows(t, base, map[string]any{"session": sess})
	if r := deep["deep0"]; r.Parent == nil || r.Parent.Window != gen1 || r.Root == nil || r.Root.Window != gen2 || r.Depth != 2 {
		t.Errorf("ASSERTION: --parent gave deep0 parent %+v, root %+v, depth %d; want %s, %s, 2", r.Parent, r.Root, r.Depth, gen1, gen2)
	}
	if r := deep["deep5"]; r.Depth != 7 {
		t.Errorf("ASSERTION: deep5 is at depth %d, want 7", r.Depth)
	}
	out, err := handoffCLI(t, base, 60*time.Second, "start-agent", "-s", sess, agent+" 0", "--name", "deep6", "--parent", parent)
	if err != nil {
		t.Errorf("ASSERTION: depth 8 was refused: %v\n%s", err, out)
	}
	parent = linkRows(t, base, map[string]any{"session": sess})["deep6"].Window
	refused, err := handoffCLI(t, base, 60*time.Second, "start-agent", "-s", sess, agent+" 0", "--name", "deep7", "--parent", parent)
	evidence["past_the_cap"] = refused
	if err == nil || !strings.Contains(refused, "stops at 8") {
		t.Errorf("ASSERTION: a start at depth 9 was not refused at the cap: %v\n%s", err, refused)
	}
	if _, ok := linkRows(t, base, map[string]any{"session": sess, "all": true})["deep7"]; ok {
		t.Errorf("ASSERTION: the refused start at depth 9 still opened a pane")
	}

	// A live upgrade keeps every link.
	res, upOut, err := daemonUpgrade(t, base, copyTuios(t))
	if err != nil {
		t.Fatalf("ASSERTION: daemon upgrade: %v\n%s", err, upOut)
	}
	evidence["upgrade"] = res
	rows = linkRows(t, base, map[string]any{"session": sess})
	evidence["after_upgrade"] = rows
	check("after the upgrade", rows, nil)

	// The parent closes: its child keeps the link, marked gone.
	if out, err := handoffCLI(t, base, 30*time.Second, "close-window", "-s", sess, gen1); err != nil {
		t.Fatalf("close-window gen1: %v\n%s", err, out)
	}
	gone := map[string]bool{gen1: true}
	rows = linkRows(t, base, map[string]any{"session": sess})
	evidence["after_close"] = rows
	check("after gen1 closed", rows, gone)
	text, _ := handoffCLI(t, base, 30*time.Second, "list-agents", "-s", sess)
	evidence["list_agents_text"] = text
	if !strings.Contains(text, "PARENT") || !strings.Contains(text, gen1[:8]+" (gone)") {
		t.Errorf("ASSERTION: list-agents text shows no parent column or no gone parent:\n%s", text)
	}

	// A restart restores the session, and the links with it. The panes
	// are new shells, so they are listed with all.
	if out, err := handoffCLI(t, base, 30*time.Second, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}
	if out, err := handoffCLI(t, base, 30*time.Second, "start-server"); err != nil {
		t.Fatalf("start-server: %v\n%s", err, out)
	}
	deadline = time.Now().Add(30 * time.Second)
	for {
		if _, err := callVerb(t, base, "session-info", map[string]any{"session": sess}); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: session %s was not restored after the restart", sess)
		}
		time.Sleep(200 * time.Millisecond)
	}
	rows = linkRows(t, base, map[string]any{"session": sess, "all": true})
	evidence["after_restart"] = rows
	check("after the restart", rows, gone)
	writeHandoffArtifact(t, evidence)
}
