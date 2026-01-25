//go:build freebsd || openbsd

package system

// readCPUUsage returns the CPU usage on BSD systems.
// Currently returns 0 as BSD CPU usage monitoring is not implemented.
func (c *CPUMonitor) readCPUUsage() (float64, error) {
	// TODO: Implement BSD CPU monitoring using sysctl
	return 0, nil
}
