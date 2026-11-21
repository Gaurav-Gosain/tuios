package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// GetCPUGraph returns a formatted string with CPU usage graph and percentage.
// It returns "CPU: n/a" when the last reading failed, which is always the case
// on a platform with no CPU reader, so an unmeasured machine never reads as an
// idle one.
func (m *OS) GetCPUGraph() string {
	if m.cpuUnavailable {
		return "CPU: n/a"
	}

	current := 0.0
	if len(m.CPUHistory) > 0 {
		current = m.CPUHistory[len(m.CPUHistory)-1]
	}

	var graphBuilder strings.Builder
	const maxBars = 10

	startPadding := maxBars - len(m.CPUHistory)
	if startPadding > 0 {
		graphBuilder.WriteString(strings.Repeat(" ", startPadding))
	}

	for i, usage := range m.CPUHistory {
		if i >= maxBars {
			break
		}
		height := min(int(usage/12.5), 8)

		if m.Settings.UseASCIIOnly {
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
// Cached to avoid a system call on every render.
func (m *OS) GetRAMUsage() string {
	if m.ramUnavailable {
		return "RAM: n/a"
	}
	return fmt.Sprintf("RAM:%5.1f%%", m.RAMUsage)
}

// UpdateRAMUsage updates the cached RAM usage.
func (m *OS) UpdateRAMUsage() {
	now := time.Now()
	if now.Sub(m.LastRAMUpdate) < 2*time.Second {
		return
	}

	m.LastRAMUpdate = now
	used, ok := readRAMPercent()
	m.ramUnavailable = !ok
	m.RAMUsage = used
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
		m.cpuLast, m.cpuHasLast = ticks, true
		return
	}
	usage := cpuBusyPercent(m.cpuLast, ticks)
	m.cpuLast = ticks

	if len(m.CPUHistory) >= 10 {
		m.CPUHistory = m.CPUHistory[1:]
	}
	m.CPUHistory = append(m.CPUHistory, usage)
}

type cpuTicks struct {
	idle  uint64
	total uint64
}

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
