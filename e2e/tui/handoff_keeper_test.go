package tuie2e

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The keeper of a live upgrade: the process that holds a copy of every pane's
// descriptor from before the exec until the new image is up, and brings the
// previous binary back when the new image dies first. See
// docs/design/live-handoff.md, section 7.1, and
// internal/session/handoff_keeper.go. The tests build on the helpers of
// live_handoff_test.go and live_upgrade_test.go.
//
// The daemon runs with TUIOS_TEST_HANDOFF=1, and TUIOS_TEST_HANDOFF_CRASH
// makes the new image end itself hard at a chosen point: SIGKILL, or with
// TUIOS_TEST_HANDOFF_CRASH_MODE=panic a panic in a goroutine of its own, which
// no recover covers and which leaves the in-place fallback no chance to run.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Live handoff").

// keeperProcesses is every keeper process of the daemon under base, as
// "pid ppid stat command". A keeper's command line names the previous binary,
// which is kept under base. ps reads untruncated command lines with -ww, on
// macOS and Linux alike.
func keeperProcesses(t *testing.T, base string) []string {
	t.Helper()
	raw, err := exec.Command("ps", "-A", "-ww", "-o", "pid=,ppid=,stat=,command=").Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "handoff-keeper") && strings.Contains(line, base) {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

// awaitNoKeeper waits until no keeper of the daemon under base is left, and
// returns how long that took, or the keepers still there at the timeout.
func awaitNoKeeper(t *testing.T, base string, timeout time.Duration) (time.Duration, []string) {
	t.Helper()
	start := time.Now()
	for {
		left := keeperProcesses(t, base)
		if len(left) == 0 {
			return time.Since(start), nil
		}
		if time.Since(start) > timeout {
			return time.Since(start), left
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// jsonLineOf is the first line of out that is a JSON object, decoded: the
// --json outcome of a command that then failed, whose last line is its error.
func jsonLineOf(out string) map[string]any {
	for _, line := range strings.Split(out, "\n") {
		var v map[string]any
		if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &v) == nil {
			return v
		}
	}
	return nil
}

// processRunning says whether pid is a process that has not ended. A zombie has
// ended.
func processRunning(pid int) bool {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	stat := strings.TrimSpace(string(out))
	return err == nil && stat != "" && !strings.HasPrefix(stat, "Z")
}

// TestDaemonUpgradeKeeperStandsDown upgrades the daemon with nothing going
// wrong and checks the keeper's life: it runs while the upgrade is held
// between the manifest write and the exec, as the daemon's child, and once
// the new image is up it is gone, reaped, within a second.
//
// The positive half is the keeper seen during the hold: without it, finding
// no keeper afterwards would say nothing, since a search that matched nothing
// would pass too.
func TestDaemonUpgradeKeeperStandsDown(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	second := copyTuios(t)
	const sess = "e2e-keeper"
	win := newCommandWindow(t, base, sess, "counter", false, "sh", "-c",
		`echo pid-$$; i=0; while :; do i=$((i+1)); echo s-$i; sleep 0.02; done`)
	waitHandoffPane(t, base, sess, win, regexp.MustCompile(`^pid-\d+`), 10*time.Second)
	before := panePIDs(t, base, sess)
	img := helloImage(t, base)

	_ = mustCall(t, base, "test-handoff", map[string]any{"binary": second, "hold_ms": 1500})
	during := keeperProcesses(t, base)
	after := awaitNewImage(t, base, img, 30*time.Second)
	gone, left := awaitNoKeeper(t, base, 3*time.Second)
	children := childCommands(t, after.PID)
	afterPIDs := panePIDs(t, base, sess)

	writeHandoffArtifact(t, map[string]any{
		"os": runtime.GOOS, "daemon_pid": img.PID, "daemon_pid_after": after.PID,
		"keepers_during_hold": during, "keepers_left": left, "keeper_gone_ms": gone.Milliseconds(),
		"daemon_children_after": children, "panes_before": before, "panes_after": afterPIDs,
	})
	if len(during) != 1 {
		t.Fatalf("ASSERTION: %d keepers while the upgrade was held, want 1: %q", len(during), during)
	}
	if f := strings.Fields(during[0]); len(f) < 2 || f[1] != strconv.Itoa(img.PID) {
		t.Errorf("ASSERTION: the keeper %q is not the daemon's (pid %d) child", during[0], img.PID)
	}
	if after.PID != img.PID {
		t.Errorf("ASSERTION: the daemon's pid changed from %d to %d", img.PID, after.PID)
	}
	if len(left) > 0 {
		t.Errorf("ASSERTION: a keeper is still there %s after the new image was up: %q", gone.Round(time.Millisecond), left)
	}
	for _, c := range children {
		if strings.Contains(c, "<defunct>") || strings.Contains(c, "handoff-keeper") {
			t.Errorf("ASSERTION: the daemon has a child left from the keeper: %q", c)
		}
	}
	for id, pid := range before {
		if afterPIDs[id] != pid {
			t.Errorf("ASSERTION: window %s had pid %d, and %d after the upgrade", id, pid, afterPIDs[id])
		}
	}
}

// TestDaemonUpgradeSurvivesACrashOfTheNewImage makes the image `tuios daemon
// upgrade` execs die hard before it is up, at several points, and checks the
// panes outlive it: the keeper brings the previous binary back with every
// pane, under the keeper's own pid.
//
//   - killed-at-start: SIGKILL first thing in the new image, before it has
//     read the manifest.
//   - panic-while-adopting: a panic no recover covers, with every pane's
//     emulator rebuilt and the sessions entered, before the fallback is
//     disarmed.
//   - killed-once-serving: SIGKILL after the new image has started reading
//     the panes and serving, before it releases the keeper. What it read
//     dies with it, so this case does not hold the counter to no gap.
//   - fallback-also-dies: the new image fails to adopt, the in-place fallback
//     execs the previous binary, and that image is killed too.
//
// In each: every pane keeps its pid; the counter goes on, with no gap except
// where noted; the daemon answers, from the kept tuios.prev under a new pid;
// the command reports the recovery; a pane that exits afterwards is heard
// (with its status, 9, on macOS, where kqueue reports a non-child's status);
// and no keeper is left.
func TestDaemonUpgradeSurvivesACrashOfTheNewImage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		env    []string
		gapsOK bool
	}{
		{"killed-at-start", []string{"TUIOS_TEST_HANDOFF_CRASH=start"}, false},
		{"panic-while-adopting", []string{"TUIOS_TEST_HANDOFF_CRASH=adopt", "TUIOS_TEST_HANDOFF_CRASH_MODE=panic"}, false},
		{"killed-once-serving", []string{"TUIOS_TEST_HANDOFF_CRASH=serve"}, true},
		{"fallback-also-dies", []string{"TUIOS_TEST_HANDOFF_FAIL=error", "TUIOS_TEST_HANDOFF_CRASH=fallback"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			startHandoffDaemon(t, base, tc.env...)
			second := copyTuios(t)
			const sess = "e2e-crash"
			counter := newCommandWindow(t, base, sess, "counter", false, "sh", "-c",
				`echo pid-$$; i=0; while :; do i=$((i+1)); echo k-$i; sleep 0.02; done`)
			exiter := newCommandWindow(t, base, sess, "exiter", true, "sh", "-c",
				`echo pid-$$; while read l; do [ "$l" = quit ] && exit 9; echo got-$l; done`)
			for _, w := range []string{counter, exiter} {
				waitHandoffPane(t, base, sess, w, regexp.MustCompile(`^pid-\d+`), 10*time.Second)
			}
			waitHandoffPane(t, base, sess, counter, regexp.MustCompile(`^k-50$`), 20*time.Second)
			before := panePIDs(t, base, sess)
			img := helloImage(t, base)
			ticksBefore := counterLines(mustScrollback(t, base, sess, counter), "k-")

			started := time.Now()
			_, out, upErr := daemonUpgrade(t, base, second)
			outcome := jsonLineOf(out)
			took := time.Since(started)
			after := helloImage(t, base)
			exe := executableOf(t, after.PID)
			afterPIDs := panePIDs(t, base, sess)
			stats := handoffStats(t, base)
			oldAlive := processRunning(img.PID)
			// The counter has to be seen moving under the image that came
			// back, past where it stood before the upgrade.
			waitHandoffPane(t, base, sess, counter, regexp.MustCompile(`^k-`+strconv.Itoa(lastOf(ticksBefore)+60)+`$`), 20*time.Second)
			ticks := counterLines(mustScrollback(t, base, sess, counter), "k-")
			_ = mustCall(t, base, "send-text", map[string]any{"session": sess, "window": exiter, "text": "ping\n"})
			waitHandoffPane(t, base, sess, exiter, regexp.MustCompile(`^got-ping`), 10*time.Second)
			_ = mustCall(t, base, "send-text", map[string]any{"session": sess, "window": exiter, "text": "quit\n"})
			code := windowExitCode(t, base, sess, exiter, 10*time.Second)
			gone, left := awaitNoKeeper(t, base, 3*time.Second)

			writeHandoffArtifact(t, map[string]any{
				"os": runtime.GOOS, "case": tc.name, "env": tc.env,
				"daemon_pid_before": img.PID, "daemon_pid_after": after.PID, "old_pid_alive": oldAlive,
				"executable_after": exe, "panes_before": before, "panes_after": afterPIDs,
				"upgrade_output": out, "upgrade_error": errString(upErr), "outcome": outcome, "took_ms": took.Milliseconds(),
				"stats": stats, "ticks_before_last": lastOf(ticksBefore), "ticks_checked": len(ticks), "tick_gap_at": firstGap(ticks),
				"exit_after": code, "keepers_left": left, "keeper_gone_ms": gone.Milliseconds(),
			})

			if after.PID == img.PID || oldAlive {
				t.Fatalf("ASSERTION: the daemon is pid %d after the crash (was %d, still alive %v): the image that crashed is the one answering", after.PID, img.PID, oldAlive)
			}
			if !strings.HasSuffix(exe, string(filepath.Separator)+filepath.Join("handoff", "tuios.prev")) {
				t.Errorf("ASSERTION: the daemon runs %s after the crash, want the kept tuios.prev", exe)
			}
			if rec, _ := stats["recovered"].(bool); !rec {
				t.Errorf("ASSERTION: the image does not say the keeper brought it back: %v", stats)
			}
			if rec, _ := outcome["recovered"].(bool); !rec || upErr == nil || !strings.Contains(out, "died before it was up") {
				t.Errorf("ASSERTION: tuios daemon upgrade did not report the recovery (recovered %v, error %v):\n%s", outcome["recovered"], upErr, out)
			}
			for id, pid := range before {
				if afterPIDs[id] != pid {
					t.Errorf("ASSERTION: window %s had pid %d, and %d after the crash", id, pid, afterPIDs[id])
				}
			}
			if len(ticks) == 0 || ticks[0] > lastOf(ticksBefore) {
				t.Errorf("ASSERTION: the counter's output does not span the crash (last before %d, after %v)", lastOf(ticksBefore), ticks)
			} else if i := firstGap(ticks); i >= 0 && !tc.gapsOK {
				t.Errorf("ASSERTION: the counter jumps from %d to %d", ticks[i-1], ticks[i])
			}
			if runtime.GOOS == "darwin" && code != 9 {
				t.Errorf("ASSERTION: the pane that exited 9 after the crash reads %d", code)
			}
			if len(left) > 0 {
				t.Errorf("ASSERTION: a keeper is still there after the recovery: %q", left)
			}
		})
	}
}
