package testutil

import (
	"os"
	"path/filepath"
)

const watcherKind = "inotify instances"

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

func startPoller() {}
