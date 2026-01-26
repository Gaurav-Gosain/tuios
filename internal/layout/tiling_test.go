package layout

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestCalculateTilingLayout_SingleWindow tests layout with one window
func TestCalculateTilingLayout_SingleWindow(t *testing.T) {
	layouts := CalculateTilingLayout(1, 200, 100, 0, 0.5)

	if len(layouts) != 1 {
		t.Fatalf("Expected 1 layout, got %d", len(layouts))
	}

	layout := layouts[0]
	if layout.X != 0 || layout.Y != 0 {
		t.Errorf("Expected position (0, 0), got (%d, %d)", layout.X, layout.Y)
	}
	if layout.Width != 200 || layout.Height != 100 {
		t.Errorf("Expected size (200, 100), got (%d, %d)", layout.Width, layout.Height)
	}
}

// TestCalculateTilingLayout_TwoWindows tests layout with two windows side by side
func TestCalculateTilingLayout_TwoWindows(t *testing.T) {
	tests := []struct {
		name        string
		masterRatio float64
		expectLeft  int
		expectRight int
	}{
		{"50-50 split", 0.5, 100, 100},
		{"60-40 split", 0.6, 120, 80},
		{"30-70 split", 0.3, 60, 140},
		{"70-30 split", 0.7, 140, 60},
		{"Clamped low", 0.2, 60, 140},  // Should clamp to 0.3
		{"Clamped high", 0.9, 140, 60}, // Should clamp to 0.7
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layouts := CalculateTilingLayout(2, 200, 100, 0, tt.masterRatio)

			if len(layouts) != 2 {
				t.Fatalf("Expected 2 layouts, got %d", len(layouts))
			}

			// Check left window
			if layouts[0].X != 0 || layouts[0].Y != 0 {
				t.Errorf("Left window: expected position (0, 0), got (%d, %d)",
					layouts[0].X, layouts[0].Y)
			}
			if layouts[0].Width != tt.expectLeft {
				t.Errorf("Left window: expected width %d, got %d",
					tt.expectLeft, layouts[0].Width)
			}
			if layouts[0].Height != 100 {
				t.Errorf("Left window: expected height 100, got %d", layouts[0].Height)
			}

			// Check right window
			if layouts[1].X != tt.expectLeft {
				t.Errorf("Right window: expected X=%d, got %d",
					tt.expectLeft, layouts[1].X)
			}
			if layouts[1].Width != tt.expectRight {
				t.Errorf("Right window: expected width %d, got %d",
					tt.expectRight, layouts[1].Width)
			}

			// Verify windows cover full width
			totalWidth := layouts[0].Width + layouts[1].Width
			if totalWidth != 200 {
				t.Errorf("Total width should be 200, got %d", totalWidth)
			}
		})
	}
}

// TestCalculateTilingLayout_ThreeWindows tests layout with three windows
func TestCalculateTilingLayout_ThreeWindows(t *testing.T) {
	layouts := CalculateTilingLayout(3, 200, 100, 0, 0.5)

	if len(layouts) != 3 {
		t.Fatalf("Expected 3 layouts, got %d", len(layouts))
	}

	// Left master window should be full height
	if layouts[0].Width != 100 {
		t.Errorf("Master window: expected width 100, got %d", layouts[0].Width)
	}
	if layouts[0].Height != 100 {
		t.Errorf("Master window: expected height 100, got %d", layouts[0].Height)
	}

	// Right two windows should be stacked
	if layouts[1].X != 100 || layouts[2].X != 100 {
		t.Error("Right windows should both start at X=100")
	}

	// Heights should add up to total
	rightTotalHeight := layouts[1].Height + layouts[2].Height
	if rightTotalHeight != 100 {
		t.Errorf("Right windows heights should sum to 100, got %d", rightTotalHeight)
	}
}

// TestCalculateTilingLayout_FourWindows tests 2x2 grid layout
func TestCalculateTilingLayout_FourWindows(t *testing.T) {
	layouts := CalculateTilingLayout(4, 200, 100, 0, 0.5)

	if len(layouts) != 4 {
		t.Fatalf("Expected 4 layouts, got %d", len(layouts))
	}

	// Should create a 2x2 grid
	expectedPositions := [][2]int{
		{0, 0},    // Top-left
		{100, 0},  // Top-right
		{0, 50},   // Bottom-left
		{100, 50}, // Bottom-right
	}

	for i, expected := range expectedPositions {
		if layouts[i].X != expected[0] || layouts[i].Y != expected[1] {
			t.Errorf("Window %d: expected position (%d, %d), got (%d, %d)",
				i, expected[0], expected[1], layouts[i].X, layouts[i].Y)
		}
	}
}

// TestCalculateTilingLayout_ManyWindows tests grid layout with many windows
func TestCalculateTilingLayout_ManyWindows(t *testing.T) {
	tests := []struct {
		name         string
		numWindows   int
		expectedCols int
		expectedRows int
	}{
		{"5 windows", 5, 2, 3},
		{"6 windows", 6, 2, 3},
		{"7 windows", 7, 3, 3},
		{"9 windows", 9, 3, 3},
		{"10 windows", 10, 3, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layouts := CalculateTilingLayout(tt.numWindows, 300, 200, 0, 0.5)

			if len(layouts) != tt.numWindows {
				t.Fatalf("Expected %d layouts, got %d", tt.numWindows, len(layouts))
			}

			// Verify all layouts are created
			for i, layout := range layouts {
				if layout.Width <= 0 || layout.Height <= 0 {
					t.Errorf("Window %d has invalid dimensions: %dx%d",
						i, layout.Width, layout.Height)
				}
				if layout.X < 0 || layout.Y < 0 {
					t.Errorf("Window %d has invalid position: (%d, %d)",
						i, layout.X, layout.Y)
				}
			}
		})
	}
}

// TestCalculateTilingLayout_MinimumSize tests that minimum sizes are enforced
func TestCalculateTilingLayout_MinimumSize(t *testing.T) {
	// Create a very small screen to test minimum size enforcement
	layouts := CalculateTilingLayout(2, 50, 20, 0, 0.5)

	if len(layouts) != 2 {
		t.Fatalf("Expected 2 layouts, got %d", len(layouts))
	}

	for i, layout := range layouts {
		if layout.Width < config.DefaultWindowWidth {
			t.Errorf("Window %d width %d is below minimum %d",
				i, layout.Width, config.DefaultWindowWidth)
		}
		if layout.Height < config.DefaultWindowHeight {
			t.Errorf("Window %d height %d is below minimum %d",
				i, layout.Height, config.DefaultWindowHeight)
		}
	}
}

// TestCalculateTilingLayout_WithTopMargin tests layout with top margin
func TestCalculateTilingLayout_WithTopMargin(t *testing.T) {
	topMargin := 2
	layouts := CalculateTilingLayout(2, 200, 100, topMargin, 0.5)

	if len(layouts) != 2 {
		t.Fatalf("Expected 2 layouts, got %d", len(layouts))
	}

	// Both windows should start at topMargin
	for i, layout := range layouts {
		if layout.Y != topMargin {
			t.Errorf("Window %d: expected Y=%d, got %d", i, topMargin, layout.Y)
		}
	}
}

// TestCalculateTilingLayout_ZeroWindows tests edge case with no windows
func TestCalculateTilingLayout_ZeroWindows(t *testing.T) {
	layouts := CalculateTilingLayout(0, 200, 100, 0, 0.5)

	if layouts != nil {
		t.Errorf("Expected nil for 0 windows, got %d layouts", len(layouts))
	}
}

// BenchmarkCalculateTilingLayout benchmarks the tiling calculation
func BenchmarkCalculateTilingLayout(b *testing.B) {
	for b.Loop() {
		_ = CalculateTilingLayout(10, 1920, 1080, 0, 0.5)
	}
}

// BenchmarkCalculateTilingLayout_ManyWindows benchmarks with many windows
func BenchmarkCalculateTilingLayout_ManyWindows(b *testing.B) {
	for b.Loop() {
		_ = CalculateTilingLayout(50, 1920, 1080, 0, 0.5)
	}
}
