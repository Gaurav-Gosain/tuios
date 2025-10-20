//go:build linux

package system

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

func getCPUStats() *CPUStats {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				return nil
			}

			stats := &CPUStats{}
			stats.user, _ = strconv.ParseUint(fields[1], 10, 64)
			stats.nice, _ = strconv.ParseUint(fields[2], 10, 64)
			stats.system, _ = strconv.ParseUint(fields[3], 10, 64)
			stats.idle, _ = strconv.ParseUint(fields[4], 10, 64)

			if len(fields) > 5 {
				stats.iowait, _ = strconv.ParseUint(fields[5], 10, 64)
			}
			if len(fields) > 6 {
				stats.irq, _ = strconv.ParseUint(fields[6], 10, 64)
			}
			if len(fields) > 7 {
				stats.softirq, _ = strconv.ParseUint(fields[7], 10, 64)
			}
			if len(fields) > 8 {
				stats.steal, _ = strconv.ParseUint(fields[8], 10, 64)
			}

			return stats
		}
	}

	return nil
}

func (c *CPUMonitor) readCPUUsage() (float64, error) {
	stats := getCPUStats()
	if stats == nil {
		return 0, nil
	}

	if c.lastCPUStats == nil {
		c.lastCPUStats = stats
		return 0, nil
	}

	// Calculate deltas
	totalDelta := float64((stats.user + stats.nice + stats.system + stats.idle + stats.iowait +
		stats.irq + stats.softirq + stats.steal) -
		(c.lastCPUStats.user + c.lastCPUStats.nice + c.lastCPUStats.system + c.lastCPUStats.idle +
			c.lastCPUStats.iowait + c.lastCPUStats.irq + c.lastCPUStats.softirq + c.lastCPUStats.steal))

	idleDelta := float64(stats.idle - c.lastCPUStats.idle)

	if totalDelta == 0 {
		return 0, nil
	}

	usage := 100.0 * (1.0 - idleDelta/totalDelta)
	c.lastCPUStats = stats

	if usage < 0 {
		return 0, nil
	}
	if usage > 100 {
		return 100, nil
	}

	return usage, nil
}
