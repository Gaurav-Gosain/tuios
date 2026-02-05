package system

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestNewCPUMonitor tests the constructor.
func TestNewCPUMonitor(t *testing.T) {
	monitor := NewCPUMonitor()

	if monitor == nil {
		t.Fatal("NewCPUMonitor returned nil")
	}
	if monitor.CPUHistory == nil {
		t.Error("CPUHistory should be initialized")
	}
	if len(monitor.CPUHistory) != 0 {
		t.Errorf("CPUHistory should be empty, got length %d", len(monitor.CPUHistory))
	}
	if cap(monitor.CPUHistory) != 10 {
		t.Errorf("CPUHistory capacity should be 10, got %d", cap(monitor.CPUHistory))
	}
	if monitor.LastCPUUpdate.IsZero() {
		t.Error("LastCPUUpdate should be initialized to current time")
	}
	if monitor.LastCPUStats != nil {
		t.Error("LastCPUStats should be nil initially")
	}
}

// TestGetCPUGraph_EmptyHistory tests graph rendering with no history.
func TestGetCPUGraph_EmptyHistory(t *testing.T) {
	monitor := NewCPUMonitor()

	graph := monitor.GetCPUGraph()

	// Format: "CPU:" (4) + graph (10) + " " (1) + percentage (4) = 19 chars
	// With empty history, should show 0%
	if !strings.HasPrefix(graph, "CPU:") {
		t.Errorf("Graph should start with 'CPU:', got %q", graph)
	}
	if !strings.HasSuffix(graph, "  0%") {
		t.Errorf("Graph should end with '  0%%' for empty history, got %q", graph)
	}
	// Should have padding for empty history (10 spaces)
	if !strings.Contains(graph, "          ") {
		t.Error("Graph should contain 10 spaces for empty history padding")
	}
}

// TestGetCPUGraph_FixedWidth tests that graph output is always fixed width.
func TestGetCPUGraph_FixedWidth(t *testing.T) {
	tests := []struct {
		name    string
		history []float64
	}{
		{"empty", []float64{}},
		{"one_sample", []float64{50.0}},
		{"five_samples", []float64{10.0, 20.0, 30.0, 40.0, 50.0}},
		{"ten_samples", []float64{10.0, 20.0, 30.0, 40.0, 50.0, 60.0, 70.0, 80.0, 90.0, 100.0}},
		{"all_zeros", []float64{0.0, 0.0, 0.0, 0.0, 0.0}},
		{"all_max", []float64{100.0, 100.0, 100.0, 100.0, 100.0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			monitor := NewCPUMonitor()
			monitor.CPUHistory = tt.history

			graph := monitor.GetCPUGraph()

			// Count runes to handle multi-byte Unicode characters
			graphSection := strings.TrimPrefix(graph, "CPU:")
			graphSection = strings.TrimSuffix(graphSection, graph[len(graph)-5:]) // Remove " XXX%"

			// The graph section should be exactly 10 runes
			graphRunes := utf8.RuneCountInString(graphSection)
			if graphRunes != 10 {
				t.Errorf("Graph section should be 10 runes, got %d for %q", graphRunes, graphSection)
			}
		})
	}
}

// TestGetCPUGraph_UsageBoundaries tests graph characters for different usage levels.
// The formula is: height = min(int(usage/12.5), 8)
// height 0 -> "▁", height 1 -> "▂", height 2 -> "▃", height 3 -> "▄",
// height 4 -> "▅", height 5 -> "▆", height 6 -> "▇", height 7,8 -> "█"
func TestGetCPUGraph_UsageBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		usage    float64
		expected string
	}{
		{"0%", 0.0, "▁"},     // 0/12.5 = 0 -> height 0
		{"12%", 12.0, "▁"},   // 12/12.5 = 0.96 -> height 0
		{"13%", 13.0, "▂"},   // 13/12.5 = 1.04 -> height 1
		{"25%", 25.0, "▃"},   // 25/12.5 = 2 -> height 2
		{"37%", 37.0, "▃"},   // 37/12.5 = 2.96 -> height 2
		{"38%", 38.0, "▄"},   // 38/12.5 = 3.04 -> height 3
		{"50%", 50.0, "▅"},   // 50/12.5 = 4 -> height 4
		{"62%", 62.0, "▅"},   // 62/12.5 = 4.96 -> height 4
		{"63%", 63.0, "▆"},   // 63/12.5 = 5.04 -> height 5
		{"75%", 75.0, "▇"},   // 75/12.5 = 6 -> height 6
		{"87%", 87.0, "▇"},   // 87/12.5 = 6.96 -> height 6
		{"88%", 88.0, "█"},   // 88/12.5 = 7.04 -> height 7
		{"100%", 100.0, "█"}, // 100/12.5 = 8 -> height 8
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			monitor := NewCPUMonitor()
			monitor.CPUHistory = []float64{tt.usage}

			graph := monitor.GetCPUGraph()

			// The graph should contain the expected character
			if !strings.Contains(graph, tt.expected) {
				t.Errorf("Graph for %.0f%% should contain %q, got %q", tt.usage, tt.expected, graph)
			}
		})
	}
}

// TestGetCPUGraph_CurrentUsageDisplay tests the percentage display.
func TestGetCPUGraph_CurrentUsageDisplay(t *testing.T) {
	tests := []struct {
		name     string
		history  []float64
		expected string
	}{
		{"0%", []float64{0.0}, "  0%"},
		{"50%", []float64{50.0}, " 50%"},
		{"100%", []float64{100.0}, "100%"},
		{"last_value", []float64{10.0, 20.0, 75.0}, " 75%"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			monitor := NewCPUMonitor()
			monitor.CPUHistory = tt.history

			graph := monitor.GetCPUGraph()

			if !strings.HasSuffix(graph, tt.expected) {
				t.Errorf("Graph should end with %q, got %q", tt.expected, graph)
			}
		})
	}
}

// TestUpdateCPUHistory_RollingWindow tests that history maintains max 10 samples.
func TestUpdateCPUHistory_RollingWindow(t *testing.T) {
	monitor := NewCPUMonitor()
	// Set LastCPUUpdate to past so updates are allowed
	monitor.LastCPUUpdate = time.Now().Add(-1 * time.Second)

	// Simulate 15 updates by directly manipulating history
	for i := range 15 {
		monitor.CPUHistory = append(monitor.CPUHistory, float64(i*10))
		if len(monitor.CPUHistory) > 10 {
			monitor.CPUHistory = monitor.CPUHistory[1:]
		}
	}

	if len(monitor.CPUHistory) != 10 {
		t.Errorf("CPUHistory should be limited to 10 samples, got %d", len(monitor.CPUHistory))
	}
	// First value should be 50 (started at 0, after 15 iterations, oldest is 5*10=50)
	if monitor.CPUHistory[0] != 50.0 {
		t.Errorf("First history value should be 50, got %.0f", monitor.CPUHistory[0])
	}
	// Last value should be 140 (14*10)
	if monitor.CPUHistory[9] != 140.0 {
		t.Errorf("Last history value should be 140, got %.0f", monitor.CPUHistory[9])
	}
}

// TestUpdateCPUHistory_Throttling tests the 500ms update throttle.
func TestUpdateCPUHistory_Throttling(t *testing.T) {
	monitor := NewCPUMonitor()

	// First update should work (LastCPUUpdate is set to time.Now() in constructor)
	initialLen := len(monitor.CPUHistory)

	// Force LastCPUUpdate to be in the past
	monitor.LastCPUUpdate = time.Now().Add(-1 * time.Second)
	monitor.UpdateCPUHistory()

	if len(monitor.CPUHistory) != initialLen+1 {
		t.Error("UpdateCPUHistory should add a sample after throttle period")
	}

	// Immediate second call should be throttled
	currentLen := len(monitor.CPUHistory)
	monitor.UpdateCPUHistory()

	if len(monitor.CPUHistory) != currentLen {
		t.Error("UpdateCPUHistory should be throttled within 500ms")
	}
}

// TestUpdateCPUHistory_AfterThrottleWindow tests update after waiting.
func TestUpdateCPUHistory_AfterThrottleWindow(t *testing.T) {
	monitor := NewCPUMonitor()
	monitor.LastCPUUpdate = time.Now().Add(-600 * time.Millisecond)

	monitor.UpdateCPUHistory()
	firstLen := len(monitor.CPUHistory)

	// Simulate time passing
	monitor.LastCPUUpdate = time.Now().Add(-600 * time.Millisecond)
	monitor.UpdateCPUHistory()

	if len(monitor.CPUHistory) != firstLen+1 {
		t.Error("UpdateCPUHistory should add sample after throttle window")
	}
}

// TestCPUStats_Struct tests the CPUStats struct fields.
func TestCPUStats_Struct(t *testing.T) {
	stats := CPUStats{
		User:    1000,
		Nice:    200,
		System:  300,
		Idle:    5000,
		Iowait:  100,
		Irq:     50,
		Softirq: 25,
		Steal:   10,
	}

	if stats.User != 1000 {
		t.Errorf("User should be 1000, got %d", stats.User)
	}
	if stats.Nice != 200 {
		t.Errorf("Nice should be 200, got %d", stats.Nice)
	}
	if stats.System != 300 {
		t.Errorf("System should be 300, got %d", stats.System)
	}
	if stats.Idle != 5000 {
		t.Errorf("Idle should be 5000, got %d", stats.Idle)
	}
	if stats.Iowait != 100 {
		t.Errorf("Iowait should be 100, got %d", stats.Iowait)
	}
	if stats.Irq != 50 {
		t.Errorf("Irq should be 50, got %d", stats.Irq)
	}
	if stats.Softirq != 25 {
		t.Errorf("Softirq should be 25, got %d", stats.Softirq)
	}
	if stats.Steal != 10 {
		t.Errorf("Steal should be 10, got %d", stats.Steal)
	}
}

// TestGetCPUGraph_MultipleHistoryValues tests graph with varying history.
func TestGetCPUGraph_MultipleHistoryValues(t *testing.T) {
	monitor := NewCPUMonitor()
	monitor.CPUHistory = []float64{0.0, 25.0, 50.0, 75.0, 100.0}

	graph := monitor.GetCPUGraph()

	// Should contain the prefix
	if !strings.HasPrefix(graph, "CPU:") {
		t.Errorf("Graph should start with 'CPU:', got %q", graph)
	}

	// Should show the last value (100%)
	if !strings.HasSuffix(graph, "100%") {
		t.Errorf("Graph should show last value 100%%, got %q", graph)
	}

	// Should contain different bar heights for varying usage
	// With 5 samples, should have 5 spaces padding + 5 bar chars
	if !strings.Contains(graph, "     ") {
		t.Error("Graph should have padding for less than 10 samples")
	}
}

// TestGetCPUGraph_ExactlyTenSamples tests graph with exactly 10 samples.
func TestGetCPUGraph_ExactlyTenSamples(t *testing.T) {
	monitor := NewCPUMonitor()
	monitor.CPUHistory = []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}

	graph := monitor.GetCPUGraph()

	// Should not have multiple padding spaces after "CPU:" (indicating empty samples)
	afterPrefix := strings.TrimPrefix(graph, "CPU:")
	if strings.HasPrefix(afterPrefix, "  ") {
		t.Error("Graph has unexpected padding spaces indicating empty samples")
	}

	// Count the bar characters (they should be exactly 10 Unicode block chars)
	barSection := graph[4 : len(graph)-5] // Remove "CPU:" prefix and " XXX%" suffix
	runeCount := utf8.RuneCountInString(barSection)
	if runeCount != 10 {
		t.Errorf("Should have exactly 10 bar characters, got %d", runeCount)
	}
}

// TestGetCPUGraph_MoreThanTenSamples tests that only 10 samples are displayed.
func TestGetCPUGraph_MoreThanTenSamples(t *testing.T) {
	monitor := NewCPUMonitor()
	// Create history with more than 10 samples
	monitor.CPUHistory = []float64{5, 10, 15, 20, 25, 30, 35, 40, 45, 50, 55, 60}

	graph := monitor.GetCPUGraph()

	// Should still only display 10 bars
	barSection := graph[4 : len(graph)-5]
	runeCount := utf8.RuneCountInString(barSection)
	if runeCount != 10 {
		t.Errorf("Should display only 10 bar characters even with more history, got %d", runeCount)
	}
}

// TestCPUMonitor_InitialState tests the initial state after construction.
func TestCPUMonitor_InitialState(t *testing.T) {
	before := time.Now()
	monitor := NewCPUMonitor()
	after := time.Now()

	if monitor.LastCPUUpdate.Before(before) || monitor.LastCPUUpdate.After(after) {
		t.Error("LastCPUUpdate should be set to approximately current time")
	}

	if monitor.LastCPUStats != nil {
		t.Error("LastCPUStats should be nil initially")
	}

	if len(monitor.CPUHistory) != 0 {
		t.Error("CPUHistory should be empty initially")
	}
}

// BenchmarkGetCPUGraph benchmarks the graph rendering.
func BenchmarkGetCPUGraph(b *testing.B) {
	monitor := NewCPUMonitor()
	monitor.CPUHistory = []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}

	for b.Loop() {
		_ = monitor.GetCPUGraph()
	}
}

// BenchmarkUpdateCPUHistory benchmarks the history update logic.
func BenchmarkUpdateCPUHistory(b *testing.B) {
	monitor := NewCPUMonitor()

	for b.Loop() {
		// Reset for each iteration to test the logic
		monitor.LastCPUUpdate = time.Now().Add(-1 * time.Second)
		monitor.UpdateCPUHistory()
		// Keep history at 10 to match normal operation
		if len(monitor.CPUHistory) > 10 {
			monitor.CPUHistory = monitor.CPUHistory[len(monitor.CPUHistory)-10:]
		}
	}
}
