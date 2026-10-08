//go:build !linux && !darwin && !windows && !freebsd && !openbsd

package app

// readCPUTicks has no reader on this platform, so the dock shows "CPU: n/a".
func readCPUTicks() (cpuTicks, bool) {
	return cpuTicks{}, false
}
