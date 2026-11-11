package testutil

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// watcherKind names what watcherInstances counts here.
const watcherKind = "kqueues"

// watcherInstances counts the kqueues this process holds open, which is what a
// file watcher is on macOS. Go's own poller is one too; it is started before
// the first count (see startPoller), so it is in both counts. lsof is the one
// tool that names a descriptor's kind here; without it the count is zero and
// the check passes.
func watcherInstances() int {
	out, err := exec.Command("lsof", "-n", "-P", "-p", strconv.Itoa(os.Getpid()), "-F", "t").Output()
	if err != nil {
		return 0
	}
	return strings.Count(string(out), "\ntKQUEUE")
}

// startPoller makes the runtime open its own kqueue now, so a test that is the
// first to use the poller is not counted as opening a watcher.
func startPoller() {
	if r, w, err := os.Pipe(); err == nil {
		_ = r.Close()
		_ = w.Close()
	}
}
