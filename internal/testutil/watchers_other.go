//go:build !linux

package testutil

// inotifyInstances is zero where there is no inotify, so the check that a
// test binary closes its watchers runs on Linux only, where they run out.
func inotifyInstances() int { return 0 }
