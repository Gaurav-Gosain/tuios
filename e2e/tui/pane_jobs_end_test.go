package tuie2e

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestPaneJobsEndWithThePane closes a pane of a client with no daemon while
// its shell runs a background job, a loop that writes to the terminal the
// way a prompt does, and checks that the job ends with the pane.
//
// The kernel's hangup, when the pane's terminal closes, reaches only the
// foreground process group. The job is in a group of its own, so before the
// client hung up the pane's session, the job outlived the pane: every write
// to the dead terminal failed with EIO, and the loop ran on. A sim run left
// one such loop running for 28 hours.
//
// How this could pass wrongly, written down first:
//   - The pid file could name the shell, not the job. $! is the job's pid.
//   - The job could end on its own. It loops until a signal ends it.
//   - A zombie could read as gone, or as alive. The job's parent is the
//     shell, which the close kills, so the job is reaped by init; the check
//     reads the state and counts a zombie as ended.
//
// NEGATIVE CONTROL: with the ptyspawn.HangUp call in Window.Close cut, the
// job still runs after the close and the test fails.
func TestPaneJobsEndWithThePane(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	term, base := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "a pane for the job")
	enterTerminalMode(t, term)
	pidFile := filepath.Join(base, "job.pid")
	runInShell(t, term, "(while :; do printf '\\033[2 q'; sleep 0.2; done) & echo $! > "+pidFile+"; echo JOB''STARTED", "JOBSTARTED", shellTimeout)
	raw := waitFileHas(t, pidFile, "the shell never wrote the job's pid", uiTimeout, func(s string) bool { return strings.TrimSpace(s) != "" })
	pid, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		t.Fatalf("the pid file holds %q", raw)
	}
	if !procAlive(pid) {
		t.Fatalf("the job %d is not running before the close", pid)
	}
	leaveTerminalMode(t, term)
	closeFocusedWindow(t, term, 0)
	waitUntil(t, "the pane's background job to end with the pane", 5*time.Second, func() (bool, string) {
		return !procAlive(pid), "the job still runs: " + describeProc(pid)
	})
}

// procAlive reports whether pid runs and is no zombie.
func procAlive(pid int) bool {
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	// The state follows the command name, which is in parentheses.
	s := string(stat)
	i := strings.LastIndexByte(s, ')')
	return i < 0 || i+2 >= len(s) || s[i+2] != 'Z'
}
