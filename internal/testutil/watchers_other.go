//go:build !linux && !darwin

package testutil

// watcherKind names what watcherInstances counts here.
const watcherKind = "watchers"

// watcherInstances is zero where there is no count to take, so the check that
// a test binary closes its watchers passes there. It runs on Linux, where the
// watchers run out, and on macOS.
func watcherInstances() int { return 0 }

func startPoller() {}
