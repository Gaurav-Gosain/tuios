package app

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

// GetCPUGraph returns a formatted string with CPU usage graph and percentage.
func (m *OS) GetCPUGraph() string {
	// Get current usage
	current := 0.0
	if len(m.CPUHistory) > 0 {
		current = m.CPUHistory[len(m.CPUHistory)-1]
	}

	// Create a mini bar graph
	var graphBuilder strings.Builder
	const maxBars = 10

	// If we have less samples, pad with spaces on the left
	startPadding := maxBars - len(m.CPUHistory)
	if startPadding > 0 {
		graphBuilder.WriteString(strings.Repeat(" ", startPadding))
	}

	// Add the actual graph bars
	for i, usage := range m.CPUHistory {
		if i >= maxBars {
			break
		}
		// Convert to 0-8 scale for vertical bars
		height := min(int(usage/12.5), 8)

		// Use block characters for the graph (or ASCII equivalents)
		if config.UseASCIIOnly {
			// ASCII fallback: use simple characters
			switch height {
			case 0:
				graphBuilder.WriteRune('_')
			case 1, 2:
				graphBuilder.WriteRune('.')
			case 3, 4:
				graphBuilder.WriteRune(':')
			case 5, 6:
				graphBuilder.WriteRune('|')
			case 7, 8:
				graphBuilder.WriteRune('#')
			}
		} else {
			// Unicode block characters
			switch height {
			case 0:
				graphBuilder.WriteRune('▁')
			case 1:
				graphBuilder.WriteRune('▂')
			case 2:
				graphBuilder.WriteRune('▃')
			case 3:
				graphBuilder.WriteRune('▄')
			case 4:
				graphBuilder.WriteRune('▅')
			case 5:
				graphBuilder.WriteRune('▆')
			case 6:
				graphBuilder.WriteRune('▇')
			case 7, 8:
				graphBuilder.WriteRune('█')
			}
		}
	}

	return fmt.Sprintf("CPU:%s %3.0f%%", graphBuilder.String(), current)
}

// GetRAMUsage returns RAM usage as a formatted string.
// Cached to avoid expensive gopsutil calls on every render.
func (m *OS) GetRAMUsage() string {
	return fmt.Sprintf("RAM:%5.1f%%", m.RAMUsage)
}

// UpdateRAMUsage updates the cached RAM usage.
func (m *OS) UpdateRAMUsage() {
	now := time.Now()
	// Update every 2 seconds (RAM changes slowly)
	if now.Sub(m.LastRAMUpdate) < 2*time.Second {
		return
	}

	m.LastRAMUpdate = now
	v, err := mem.VirtualMemory()
	if err != nil {
		m.RAMUsage = 0
		return
	}
	m.RAMUsage = v.UsedPercent
}

// UpdateCPUHistory updates the CPU usage history.
// This is a placeholder implementation that maintains the existing CPU history structure.
// In the future, this should be refactored to use the system.CPUMonitor.
func (m *OS) UpdateCPUHistory() {
	now := time.Now()
	// Update every 500ms (as defined in config.CPUUpdateInterval)
	if now.Sub(m.LastCPUUpdate) < config.CPUUpdateInterval {
		return
	}

	m.LastCPUUpdate = now
	// For now, we'll use a simple placeholder value
	// In a full refactor, this would use system.CPUMonitor or directly call platform-specific functions
	usage := getCPUUsageSimple()

	// Keep last 10 samples for a compact graph
	if len(m.CPUHistory) >= 10 {
		m.CPUHistory = m.CPUHistory[1:]
	}
	m.CPUHistory = append(m.CPUHistory, usage)
}

// CPUStats holds CPU usage statistics.
type CPUStats struct {
	user    uint64
	nice    uint64
	system  uint64
	idle    uint64
	iowait  uint64
	irq     uint64
	softirq uint64
	steal   uint64
}

var lastCPUStats *CPUStats

// getCPUUsageSimple retrieves current CPU usage as a percentage.
// This is a platform-specific implementation.
func getCPUUsageSimple() float64 {
	switch runtime.GOOS {
	case "linux":
		return getCPUUsageLinux()
	case "darwin":
		return getCPUUsageDarwin()
	default:
		return 0.0
	}
}

// getCPUUsageLinux retrieves CPU usage on Linux systems.
func getCPUUsageLinux() float64 {
	stats := getCPUStats()
	if stats == nil {
		return 0
	}

	if lastCPUStats == nil {
		lastCPUStats = stats
		return 0
	}

	// Calculate deltas
	totalDelta := float64((stats.user + stats.nice + stats.system + stats.idle + stats.iowait +
		stats.irq + stats.softirq + stats.steal) -
		(lastCPUStats.user + lastCPUStats.nice + lastCPUStats.system + lastCPUStats.idle +
			lastCPUStats.iowait + lastCPUStats.irq + lastCPUStats.softirq + lastCPUStats.steal))

	idleDelta := float64(stats.idle - lastCPUStats.idle)

	if totalDelta == 0 {
		return 0
	}

	usage := 100.0 * (1.0 - idleDelta/totalDelta)
	lastCPUStats = stats

	if usage < 0 {
		return 0
	}
	if usage > 100 {
		return 100
	}

	return usage
}

// getCPUUsageDarwin retrieves CPU usage on macOS systems using gopsutil.
func getCPUUsageDarwin() float64 {
	// Get CPU percent over a short interval
	percentages, err := cpu.Percent(100*time.Millisecond, false)
	if err != nil || len(percentages) == 0 {
		return 0
	}
	return percentages[0]
}

// getCPUStats reads CPU statistics from /proc/stat (Linux only).
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
