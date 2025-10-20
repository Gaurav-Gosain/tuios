//go:build darwin

package system

func (c *CPUMonitor) readCPUUsage() (float64, error) {
	// macOS-specific implementation
	// For now, return 0 or implement using sysctl
	return 0, nil
}
