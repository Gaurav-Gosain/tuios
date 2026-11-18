//go:build !windows

package app

import "github.com/shirou/gopsutil/v4/mem"

// readRAMPercent reads the share of physical memory in use, from 0 to 100,
// through gopsutil. Windows has its own reader in sysinfo_windows.go: there
// gopsutil links WMI and OLE, which cost the binary bytes it cannot spare.
func readRAMPercent() (float64, bool) {
	v, err := mem.VirtualMemory()
	if err != nil {
		return 0, false
	}
	return v.UsedPercent, true
}
