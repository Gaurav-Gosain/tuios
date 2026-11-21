//go:build !linux && !darwin

package main

import "runtime"

func keepAwakeMethodCheck() (method string, ok bool, detail string) {
	if runtime.GOOS == "windows" {
		return "SetThreadExecutionState", true, ""
	}
	return "none", false, "tuios can keep only Linux with systemd, macOS and Windows awake"
}
