package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

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

// GetCPUUsage returns the current CPU usage as a percentage.
func GetCPUUsage() float64 {
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

// UpdateCPUHistory updates the CPU usage history.
func (m *OS) UpdateCPUHistory() {
	now := time.Now()
	// Update every 500ms
	if now.Sub(m.LastCPUUpdate) < time.Duration(CPUUpdateInterval)*time.Millisecond {
		return
	}

	m.LastCPUUpdate = now
	usage := GetCPUUsage()

	// Keep last 10 samples for a compact graph
	if len(m.CPUHistory) >= 10 {
		m.CPUHistory = m.CPUHistory[1:]
	}
	m.CPUHistory = append(m.CPUHistory, usage)
}

// GetCPUGraph returns a string representing the CPU usage graph.
func (m *OS) GetCPUGraph() string {
	// Always return a fixed-width string to prevent layout shifts

	// Get current usage
	current := 0.0
	if len(m.CPUHistory) > 0 {
		current = m.CPUHistory[len(m.CPUHistory)-1]
	}

	// Create a mini bar graph - always exactly 10 characters
	graph := ""

	// If we have less than 10 samples, pad with spaces on the left
	startPadding := 10 - len(m.CPUHistory)
	if startPadding > 0 {
		graph = strings.Repeat(" ", startPadding)
	}

	// Add the actual graph bars
	for i, usage := range m.CPUHistory {
		if i >= 10 { // Limit to 10 bars
			break
		}
		// Convert to 0-8 scale for vertical bars
		height := min(
			// 100/8 = 12.5
			int(usage/12.5), 8)

		// Use block characters for the graph
		switch height {
		case 0:
			graph += "▁"
		case 1:
			graph += "▂"
		case 2:
			graph += "▃"
		case 3:
			graph += "▄"
		case 4:
			graph += "▅"
		case 5:
			graph += "▆"
		case 6:
			graph += "▇"
		case 7, 8:
			graph += "█"
		}
	}

	// Fixed width format: "CPU:" (4) + graph (10) + " " (1) + percentage (4) = 19 chars total
	return fmt.Sprintf("CPU:%s %3.0f%%", graph, current)
}
