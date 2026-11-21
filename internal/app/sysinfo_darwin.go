package app

import "github.com/shirou/gopsutil/v4/cpu"

func readCPUTicks() (cpuTicks, bool) {
	times, err := cpu.Times(false)
	if err != nil || len(times) == 0 {
		return cpuTicks{}, false
	}
	t := times[0]
	return cpuTicks{
		idle:  uint64(t.Idle * 1e6),
		total: uint64((t.User + t.System + t.Idle + t.Nice) * 1e6),
	}, true
}
