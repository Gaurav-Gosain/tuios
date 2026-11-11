package testutil

import (
	"os"
	"path/filepath"
)

// watcherKind names what watcherInstances counts here.
const watcherKind = "inotify instances"

// watcherInstances counts the inotify instances this process holds open. Each
// is a file descriptor that links to anon_inode:inotify. The kernel allows 128
// per user by default, across every test binary running at once.
func watcherInstances() int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name())); err == nil && target == "anon_inode:inotify" {
			n++
		}
	}
	return n
}

// startPoller has nothing to do here: Go's poller is an epoll, not counted.
func startPoller() {}
