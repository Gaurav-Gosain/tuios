//go:build !linux && !darwin && !windows && !freebsd && !openbsd

package app

func readCPUTicks() (cpuTicks, bool) {
	return cpuTicks{}, false
}
