//go:build !windows

package app

import "github.com/shirou/gopsutil/v4/mem"

func readRAMPercent() (float64, bool) {
	v, err := mem.VirtualMemory()
	if err != nil {
		return 0, false
	}
	return v.UsedPercent, true
}
