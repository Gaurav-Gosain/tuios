//go:build windows

package system

// readCPUUsage reads CPU usage on Windows.
// For now, returns 0 as Windows CPU monitoring requires PDH or WMI APIs.
// TODO: Implement proper Windows CPU monitoring using Performance Counters.
func (c *CPUMonitor) readCPUUsage() (float64, error) {
	// Windows CPU monitoring is more complex and requires either:
	// 1. PDH (Performance Data Helper) API
	// 2. WMI queries
	// 3. GetSystemTimes() with deltas
	// For now, return 0 to avoid compilation errors
	return 0, nil
}
