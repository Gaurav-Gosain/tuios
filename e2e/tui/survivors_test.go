package tuie2e

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// survivorGrace is how long the processes of a test have to end after its
// teardown. A closing pane's program gets one second to act on the hangup
// (paneCloseGrace in internal/session), so this leaves a wide margin.
const survivorGrace = 5 * time.Second

// failSurvivors fails the test when a process it started still runs after
// its teardown, and kills that process. killDaemon registers it before the
// daemon's own stop, so it runs after the daemon, its panes and the clients
// are gone, and before t.TempDir removes base.
//
// A process is the test's when its working directory is under base, or its
// environment points HOME or an XDG folder into base: every process the suite
// starts gets those (xdgKeys), also one that changed its directory or left
// its session with setsid. A process that has begun to exit, or that belongs
// to another user, shows neither, so it is not counted. Linux only: it reads
// /proc.
//
// Left running, such a process outlives the suite: a pane's background job
// that writes to its dead terminal spins on EIO, and a helper that holds
// connections sleeps on for days.
func failSurvivors(t *testing.T, base string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		return
	}
	t.Cleanup(func() {
		deadline := time.Now().Add(survivorGrace)
		left := testProcs(base)
		for len(left) > 0 && time.Now().Before(deadline) {
			pause(t, "the test's processes end after its teardown", 100*time.Millisecond)
			left = testProcs(base)
		}
		for _, pid := range left {
			t.Errorf("ASSERTION: a process this test started still runs %s after its teardown, so it is killed now: %s", survivorGrace, describeProc(pid))
			if p, err := os.FindProcess(pid); err == nil && ownedBy(pid, base) {
				_ = p.Kill()
			}
		}
	})
}

// testProcs lists the processes other than this one that belong to the test
// with the isolation root base (ownedBy).
func testProcs(base string) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		if ownedBy(pid, base) {
			out = append(out, pid)
		}
	}
	return out
}

// ownedBy reports whether process pid works under base, or has HOME or an
// XDG folder in base.
func ownedBy(pid int, base string) bool {
	dir := filepath.Join("/proc", strconv.Itoa(pid))
	if cwd, err := os.Readlink(filepath.Join(dir, "cwd")); err == nil && under(cwd, base) {
		return true
	}
	raw, err := os.ReadFile(filepath.Join(dir, "environ"))
	if err != nil {
		return false
	}
	for kv := range bytes.SplitSeq(raw, []byte{0}) {
		k, v, ok := strings.Cut(string(kv), "=")
		if ok && (k == "HOME" || strings.HasPrefix(k, "XDG_")) && under(v, base) {
			return true
		}
	}
	return false
}

// under reports whether path is base or in it. A folder removed while a
// process works in it reads as "PATH (deleted)".
func under(path, base string) bool {
	path = strings.TrimSuffix(path, " (deleted)")
	return path == base || strings.HasPrefix(path, base+string(filepath.Separator))
}
