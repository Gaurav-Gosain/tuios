//go:build darwin

package system

/*
#include <mach/mach_host.h>
#include <mach/host_info.h>

// Get CPU ticks for all cores
int get_cpu_ticks(uint64_t *user, uint64_t *system, uint64_t *idle, uint64_t *nice) {
	host_cpu_load_info_data_t cpuinfo;
	mach_msg_type_number_t count = HOST_CPU_LOAD_INFO_COUNT;
	kern_return_t kr = host_statistics(mach_host_self(), HOST_CPU_LOAD_INFO, (host_info_t)&cpuinfo, &count);

	if (kr != KERN_SUCCESS) {
		return -1;
	}

	*user = cpuinfo.cpu_ticks[CPU_STATE_USER];
	*system = cpuinfo.cpu_ticks[CPU_STATE_SYSTEM];
	*idle = cpuinfo.cpu_ticks[CPU_STATE_IDLE];
	*nice = cpuinfo.cpu_ticks[CPU_STATE_NICE];

	return 0;
}
*/
import "C"

var lastDarwinStats *darwinCPUStats

type darwinCPUStats struct {
	user   uint64
	system uint64
	idle   uint64
	nice   uint64
}

func (c *CPUMonitor) readCPUUsage() (float64, error) {
	var user, system, idle, nice C.uint64_t

	if C.get_cpu_ticks(&user, &system, &idle, &nice) != 0 {
		return 0, nil
	}

	stats := &darwinCPUStats{
		user:   uint64(user),
		system: uint64(system),
		idle:   uint64(idle),
		nice:   uint64(nice),
	}

	if lastDarwinStats == nil {
		lastDarwinStats = stats
		return 0, nil
	}

	// Calculate deltas
	totalDelta := float64((stats.user + stats.system + stats.idle + stats.nice) -
		(lastDarwinStats.user + lastDarwinStats.system + lastDarwinStats.idle + lastDarwinStats.nice))

	idleDelta := float64(stats.idle - lastDarwinStats.idle)

	lastDarwinStats = stats

	if totalDelta == 0 {
		return 0, nil
	}

	usage := 100.0 * (1.0 - idleDelta/totalDelta)

	if usage < 0 {
		return 0, nil
	}
	if usage > 100 {
		return 100, nil
	}

	return usage, nil
}
