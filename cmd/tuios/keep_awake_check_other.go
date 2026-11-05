//go:build !linux && !darwin

package main

import "runtime"

// keepAwakeMethodCheck reports whether this system has a way to be held
// awake. Windows always has SetThreadExecutionState.
func keepAwakeMethodCheck() (method string, ok bool, detail string) {
	if runtime.GOOS == "windows" {
		return "SetThreadExecutionState", true, ""
	}
	return "none", false, "tuios can keep only Linux with systemd, macOS and Windows awake"
}
