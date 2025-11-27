// Package system provides system information and resource monitoring.
package system

import (
	"fmt"
	"strings"
	"time"
)

// CPUStats holds CPU usage statistics.
type CPUStats struct {
	User    uint64
	Nice    uint64
	System  uint64
	Idle    uint64
	Iowait  uint64
	Irq     uint64
	Softirq uint64
	Steal   uint64
}

// CPUMonitor manages CPU usage tracking and display.
type CPUMonitor struct {
	CPUHistory    []float64
	LastCPUUpdate time.Time
	LastCPUStats  *CPUStats
}

// NewCPUMonitor creates a new CPU monitor.
func NewCPUMonitor() *CPUMonitor {
	return &CPUMonitor{
		CPUHistory:    make([]float64, 0, 10),
		LastCPUUpdate: time.Now(),
	}
}

// GetCPUUsage returns the current CPU usage as a percentage.
func (c *CPUMonitor) GetCPUUsage() float64 {
	usage, err := c.readCPUUsage()
	if err != nil {
		return 0
	}
	return usage
}

// UpdateCPUHistory updates the CPU usage history.
func (c *CPUMonitor) UpdateCPUHistory() {
	now := time.Now()
	// Update every 500ms
	if now.Sub(c.LastCPUUpdate) < 500*time.Millisecond {
		return
	}

	c.LastCPUUpdate = now
	usage := c.GetCPUUsage()

	// Keep last 10 samples for a compact graph
	if len(c.CPUHistory) >= 10 {
		c.CPUHistory = c.CPUHistory[1:]
	}
	c.CPUHistory = append(c.CPUHistory, usage)
}

// GetCPUGraph returns a string representing the CPU usage graph.
func (c *CPUMonitor) GetCPUGraph() string {
	// Always return a fixed-width string to prevent layout shifts

	// Get current usage
	current := 0.0
	if len(c.CPUHistory) > 0 {
		current = c.CPUHistory[len(c.CPUHistory)-1]
	}

	// Create a mini bar graph - always exactly 10 characters
	graph := ""

	// If we have less than 10 samples, pad with spaces on the left
	startPadding := 10 - len(c.CPUHistory)
	if startPadding > 0 {
		graph = strings.Repeat(" ", startPadding)
	}

	// Add the actual graph bars
	for i, usage := range c.CPUHistory {
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

// Start begins monitoring CPU usage in the background.
func (c *CPUMonitor) Start() {
	go c.monitorLoop()
}

// monitorLoop continuously monitors CPU usage.
func (c *CPUMonitor) monitorLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		c.UpdateCPUHistory()
	}
}

// readCPUUsage is a platform-specific method to read CPU usage.
// It is implemented in platform files (sysinfo_linux.go, sysinfo_darwin.go, etc.)
