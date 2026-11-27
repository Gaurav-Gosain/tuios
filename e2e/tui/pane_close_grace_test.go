//go:build unix

package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A pane's program that saves its state on SIGHUP gets the time to do it when
// the pane closes. Before, the daemon sent SIGKILL to the pane's program right
// after the hangup, so a program run as the pane command (new-window -- PROG)
// died before it could act. A shell hides this, as the shell is the one killed.
//
// How this could pass wrongly, written down first:
//   - A save that is instant passes even with no grace, as the signal handler
//     can run before the SIGKILL lands. The saver waits 300 ms before it
//     writes, so only a grace lets it finish.
//   - A grace with no end passes the save and leaves a program that ignores
//     SIGHUP running. The deaf program must end within a few seconds.
//   - The deaf program killed at once, as before, passes "it ends". It must
//     end no sooner than 800 ms after the close. A slow machine only makes
//     that time longer.
//   - A pid reused by another program reads as "still running". A pid counts
//     only while its command line holds its marker.
//   - Only close-window covered. kill-server closes every pane while the
//     daemon stops, and must wait for the grace before it exits.
//
// The rules for jobs (nohup and setsid run on) are in TestPaneCloseEndsItsJobs.
func TestPaneCloseGivesTheProgramTimeToSave(t *testing.T) {
	requireReal(t, "asserts on OS processes (pids, signals, /proc)")
	if os.Getenv("TUIOS_E2E") == "" {
		t.Skip("set TUIOS_E2E=1 to run")
	}
	base := t.TempDir()
	killDaemon(t, base)
	dir := t.TempDir()
	cli := func(args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	var log strings.Builder
	seed := 700000 + os.Getpid()%100000*10

	// saver writes its pid, and on SIGHUP waits 300 ms, writes "saved" and
	// exits. The wait in the shell ends on the signal.
	saver := func(sess, name string, n int) (pidFile, savedFile string) {
		t.Helper()
		pidFile = filepath.Join(dir, name+".pid")
		savedFile = filepath.Join(dir, name+".saved")
		script := fmt.Sprintf("trap 'sleep 0.3; echo saved > %s; exit 0' HUP; echo $$ > %s; sleep %d & wait",
			savedFile, pidFile, seed+n)
		cli("new-window", name, "-s", sess, "--no-focus", "--", "sh", "-c", script)
		return pidFile, savedFile
	}
	// deaf ignores SIGHUP and runs until it is killed. Its marker is in its
	// own command line.
	deaf := func(sess, name string, n int) (pidFile, marker string) {
		t.Helper()
		pidFile = filepath.Join(dir, name+".pid")
		marker = "deaf-" + strconv.Itoa(seed+n)
		script := fmt.Sprintf("trap '' HUP; echo $$ > %s; while :; do sleep 1; done; : %s", pidFile, marker)
		cli("new-window", name, "-s", sess, "--no-focus", "--", "sh", "-c", script)
		return pidFile, marker
	}
	pidOf := func(file, marker string) int {
		t.Helper()
		var pid int
		waitUntil(t, "the pane program writes its pid to "+file, uiTimeout, func() (bool, string) {
			data, err := os.ReadFile(file)
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			return err == nil && pid > 0 && runsWith(pid, marker), fmt.Sprintf("pid file %q, err %v", data, err)
		})
		cmd := commandOf(pid)
		t.Cleanup(func() {
			if runsWith(pid, cmd) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		})
		return pid
	}
	saved := func(what, file string) {
		t.Helper()
		waitUntil(t, what+": the program saves on SIGHUP before it is killed", 5*time.Second, func() (bool, string) {
			data, err := os.ReadFile(file)
			return err == nil && strings.TrimSpace(string(data)) == "saved", fmt.Sprintf("%q, err %v", data, err)
		})
		fmt.Fprintf(&log, "%s: the saver wrote its file\n", what)
	}
	// ends waits for the deaf program to end and returns the time from since
	// until it was seen gone. The first look is at once, so the time seen is
	// at most one poll late.
	ends := func(what string, pid int, marker string, since time.Time) time.Duration {
		t.Helper()
		waitUntil(t, what+": the program that ignores SIGHUP is killed after the grace", 5*time.Second, func() (bool, string) {
			return !runsWith(pid, marker), commandOf(pid)
		})
		took := time.Since(since)
		fmt.Fprintf(&log, "%s: the deaf program (pid %d) ended %s after the close\n", what, pid, took.Round(time.Millisecond))
		return took
	}

	cli("new", "grace", "--detach")

	// close-window, the saver.
	sp, sf := saver("grace", "saver", 1)
	pidOf(sp, "trap")
	cli("close-window", windowIDByName(t, base, "grace", "saver"), "-s", "grace")
	saved("close-window", sf)

	// close-window, the deaf program: alive through the grace, gone after it.
	dp, dm := deaf("grace", "deaf", 2)
	dpid := pidOf(dp, dm)
	deafWin := windowIDByName(t, base, "grace", "deaf")
	closed := time.Now()
	cli("close-window", deafWin, "-s", "grace")
	if took := ends("close-window", dpid, dm, closed); took < 800*time.Millisecond {
		t.Errorf("close-window: the program that ignores SIGHUP ended %s after the close, want a grace of about a second", took)
	}

	// kill-server, both at once. The daemon waits for the grace, so the deaf
	// program is gone soon after the command returns.
	sp, sf = saver("grace", "saver2", 3)
	pidOf(sp, "trap")
	dp, dm = deaf("grace", "deaf2", 4)
	dpid = pidOf(dp, dm)
	start := time.Now()
	cli("kill-server")
	fmt.Fprintf(&log, "kill-server: returned after %s\n", time.Since(start).Round(time.Millisecond))
	saved("kill-server", sf)
	ends("kill-server", dpid, dm, start)

	path := filepath.Join(artifactDir(t), "pane-close-grace.txt")
	if err := os.WriteFile(path, []byte(log.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("steps:\n%s(saved to %s)", log.String(), path)
}

// A live upgrade right after a pane closes waits for that pane's grace, and
// touches no pane that it hands over. The new image does not know the closed
// pane, so without the wait its program would run on with no pane.
//
// How this could pass wrongly, written down first:
//   - A wait that hangs up the panes it hands over still keeps their pids.
//     The kept pane writes a file on SIGHUP, and that file must not exist.
//   - A closed program that ends on its own passes "it ends". It ignores
//     SIGHUP and only SIGKILL ends it.
func TestDaemonUpgradeWaitsForAClosedPaneOnly(t *testing.T) {
	requireReal(t, "live upgrade execs a new binary")
	if os.Getenv("TUIOS_E2E") == "" {
		t.Skip("set TUIOS_E2E=1 to run")
	}
	base := t.TempDir()
	startHandoffDaemon(t, base)
	second := copyTuios(t)
	dir := t.TempDir()
	const sess = "e2e-grace-upgrade"
	seed := 800000 + os.Getpid()%100000*10

	keepPID := filepath.Join(dir, "keep.pid")
	hung := filepath.Join(dir, "keep.hung")
	keepMarker := "keep-" + strconv.Itoa(seed+1)
	keep := newCommandWindow(t, base, sess, "keep", false, "sh", "-c",
		fmt.Sprintf("trap 'echo hung > %s' HUP; echo $$ > %s; while :; do sleep 1; done; : %s", hung, keepPID, keepMarker))
	deafPID := filepath.Join(dir, "deaf.pid")
	deafMarker := "deaf-" + strconv.Itoa(seed+2)
	deaf := newCommandWindow(t, base, sess, "deaf", false, "sh", "-c",
		fmt.Sprintf("trap '' HUP; echo $$ > %s; while :; do sleep 1; done; : %s", deafPID, deafMarker))

	pidFrom := func(file, marker string) int {
		t.Helper()
		var pid int
		waitUntil(t, "the pane program writes its pid to "+file, uiTimeout, func() (bool, string) {
			data, err := os.ReadFile(file)
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			return err == nil && pid > 0 && runsWith(pid, marker), fmt.Sprintf("pid file %q, err %v", data, err)
		})
		t.Cleanup(func() {
			if runsWith(pid, marker) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		})
		return pid
	}
	kp := pidFrom(keepPID, keepMarker)
	dp := pidFrom(deafPID, deafMarker)
	before := panePIDs(t, base, sess)

	if out, err := handoffCLI(t, base, 30*time.Second, "close-window", deaf, "-s", sess); err != nil {
		t.Fatalf("close-window: %v\n%s", err, out)
	}
	start := time.Now()
	text, err := handoffCLI(t, base, 90*time.Second, "daemon", "upgrade", "--binary", second)
	if err != nil {
		t.Fatalf("ASSERTION: tuios daemon upgrade failed: %v\n%s", err, text)
	}
	took := time.Since(start)

	waitUntil(t, "the closed pane's program that ignores SIGHUP is killed across the upgrade", 5*time.Second, func() (bool, string) {
		return !runsWith(dp, deafMarker), commandOf(dp)
	})
	after := panePIDs(t, base, sess)
	if after[keep] != before[keep] || !runsWith(kp, keepMarker) {
		t.Errorf("ASSERTION: the kept pane had pid %d, and %d after the upgrade (runs: %v)", before[keep], after[keep], runsWith(kp, keepMarker))
	}
	if data, err := os.ReadFile(hung); err == nil {
		t.Errorf("ASSERTION: the kept pane got SIGHUP across the upgrade (it wrote %q)", data)
	}
	if _, ok := after[deaf]; ok {
		t.Errorf("ASSERTION: the closed pane %s is back after the upgrade", deaf)
	}
	t.Logf("upgrade took %s; kept pane pid %d, closed program pid %d gone\n%s", took.Round(time.Millisecond), kp, dp, text)
}
