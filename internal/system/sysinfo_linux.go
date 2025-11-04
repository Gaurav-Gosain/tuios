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
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				return nil
			}

			stats := &CPUStats{}
			stats.User, _ = strconv.ParseUint(fields[1], 10, 64)
			stats.Nice, _ = strconv.ParseUint(fields[2], 10, 64)
			stats.System, _ = strconv.ParseUint(fields[3], 10, 64)
			stats.Idle, _ = strconv.ParseUint(fields[4], 10, 64)

			if len(fields) > 5 {
				stats.Iowait, _ = strconv.ParseUint(fields[5], 10, 64)
			}
			if len(fields) > 6 {
				stats.Irq, _ = strconv.ParseUint(fields[6], 10, 64)
			}
			if len(fields) > 7 {
				stats.Softirq, _ = strconv.ParseUint(fields[7], 10, 64)
			}
			if len(fields) > 8 {
				stats.Steal, _ = strconv.ParseUint(fields[8], 10, 64)
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

	if c.LastCPUStats == nil {
		c.LastCPUStats = stats
		return 0, nil
	}

	// Calculate deltas
	totalDelta := float64((stats.User + stats.Nice + stats.System + stats.Idle + stats.Iowait +
		stats.Irq + stats.Softirq + stats.Steal) -
		(c.LastCPUStats.User + c.LastCPUStats.Nice + c.LastCPUStats.System + c.LastCPUStats.Idle +
			c.LastCPUStats.Iowait + c.LastCPUStats.Irq + c.LastCPUStats.Softirq + c.LastCPUStats.Steal))

	idleDelta := float64(stats.Idle - c.LastCPUStats.Idle)

	if totalDelta == 0 {
		return 0, nil
	}

	usage := 100.0 * (1.0 - idleDelta/totalDelta)
	c.LastCPUStats = stats

	if usage < 0 {
		return 0, nil
	}
	if usage > 100 {
		return 100, nil
	}

	return usage, nil
}
