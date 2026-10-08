package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/shirou/gopsutil/v4/mem"
)

// GetCPUGraph returns a formatted string with CPU usage graph and percentage.
// It returns "CPU: n/a" when the last reading failed, which is always the case
// on a platform with no CPU reader, so an unmeasured machine never reads as an
// idle one.
func (m *OS) GetCPUGraph() string {
	if m.cpuUnavailable {
		return "CPU: n/a"
	}

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
		if m.Settings.UseASCIIOnly {
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
	if m.ramUnavailable {
		return "RAM: n/a"
	}
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
	m.ramUnavailable = err != nil
	if err != nil {
		m.RAMUsage = 0
		return
	}
	m.RAMUsage = v.UsedPercent
}

// UpdateCPUHistory samples CPU usage at most once per config.CPUUpdateInterval
// and keeps the last ten samples for the dock's graph.
func (m *OS) UpdateCPUHistory() {
	now := time.Now()
	if now.Sub(m.LastCPUUpdate) < config.CPUUpdateInterval {
		return
	}

	m.LastCPUUpdate = now
	ticks, ok := readCPUTicks()
	m.cpuUnavailable = !ok
	if !ok {
		m.cpuHasLast = false
		return
	}
	if !m.cpuHasLast {
		// The first reading is only a baseline: a percentage needs two.
		m.cpuLast, m.cpuHasLast = ticks, true
		return
	}
	usage := cpuBusyPercent(m.cpuLast, ticks)
	m.cpuLast = ticks

	// Keep last 10 samples for a compact graph
	if len(m.CPUHistory) >= 10 {
		m.CPUHistory = m.CPUHistory[1:]
	}
	m.CPUHistory = append(m.CPUHistory, usage)
}

// cpuTicks is one reading of the machine-wide cumulative CPU time counters, in
// whatever unit the platform counts. Only the difference between two readings
// means anything. Each platform's readCPUTicks (sysinfo_<goos>.go) fills it; a
// platform without one reports false and the dock shows "CPU: n/a".
type cpuTicks struct {
	idle  uint64 // time all CPUs spent idle
	total uint64 // time all CPUs spent in any state, idle included
}

// cpuBusyPercent is the share of the time between two readings that the CPUs
// spent busy, from 0 to 100. A counter that went backwards (a wrap, or a
// reading from a different processor group on Windows) yields 0 rather than
// a wild figure.
func cpuBusyPercent(prev, cur cpuTicks) float64 {
	if cur.total <= prev.total || cur.idle < prev.idle {
		return 0
	}
	total := cur.total - prev.total
	idle := cur.idle - prev.idle
	if idle >= total {
		return 0
	}
	return 100 * float64(total-idle) / float64(total)
}
