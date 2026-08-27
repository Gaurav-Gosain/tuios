package layout

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestCalculateTilingLayout_SingleWindow tests layout with one window
func TestCalculateTilingLayout_SingleWindow(t *testing.T) {
	layouts := CalculateTilingLayout(1, 200, 100, 0, 0.5, 0)

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
			layouts := CalculateTilingLayout(2, 200, 100, 0, tt.masterRatio, 0)

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
	layouts := CalculateTilingLayout(3, 200, 100, 0, 0.5, 0)

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
	layouts := CalculateTilingLayout(4, 200, 100, 0, 0.5, 0)

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
			layouts := CalculateTilingLayout(tt.numWindows, 300, 200, 0, 0.5, 0)

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

// outside reports the rectangles that leave the region, and overlapping reports
// the pairs that sit on top of each other. Between them they are the tiler's
// whole contract.
func outside(layouts []TileLayout, w, h, topMargin int) []TileLayout {
	var out []TileLayout
	for _, r := range layouts {
		if r.Width <= 0 || r.Height <= 0 ||
			r.X < 0 || r.X+r.Width > w ||
			r.Y < topMargin || r.Y+r.Height > topMargin+h {
			out = append(out, r)
		}
	}
	return out
}

func overlapping(layouts []TileLayout) [][2]TileLayout {
	var out [][2]TileLayout
	for a := range layouts {
		for b := a + 1; b < len(layouts); b++ {
			p, q := layouts[a], layouts[b]
			if p.X < q.X+q.Width && q.X < p.X+p.Width &&
				p.Y < q.Y+q.Height && q.Y < p.Y+p.Height {
				out = append(out, [2]TileLayout{p, q})
			}
		}
	}
	return out
}

// TestPanesShrinkRatherThanOverlap is the regression for the fixed pane minimum
// this tiler used to enforce. Every tile was grown to config.DefaultWindowWidth
// by config.DefaultWindowHeight and then shoved back inside the screen, so a
// region that could not give every pane that much drew them on top of each
// other.
//
// The two sizes named here are the ones it was found at: seven panes on a 51x37
// terminal came out with four overlapping pairs, and 45x14 overlapped from five
// panes up. Both are a comfortable half of a laptop screen.
func TestPanesShrinkRatherThanOverlap(t *testing.T) {
	for _, c := range []struct{ n, w, h int }{{7, 51, 35}, {5, 45, 12}} {
		layouts := CalculateTilingLayout(c.n, c.w, c.h, 1, 0.5, 0)
		if len(layouts) != c.n {
			t.Fatalf("%d panes on %dx%d produced %d rectangles", c.n, c.w, c.h, len(layouts))
		}
		for _, pair := range overlapping(layouts) {
			t.Errorf("%d panes on %dx%d: %+v and %+v overlap", c.n, c.w, c.h, pair[0], pair[1])
		}
		for _, r := range outside(layouts, c.w, c.h, 1) {
			t.Errorf("%d panes on %dx%d: %+v is outside the region", c.n, c.w, c.h, r)
		}
		// At least one pane is smaller than the old minimum on at least one
		// axis, which is the whole point: there was no room for it and the
		// tiler used to take it anyway. Without this the two sizes could drift
		// into ones the old minimum happened to fit, and the case above would
		// stop demonstrating anything.
		narrowest, shortest := layouts[0].Width, layouts[0].Height
		for _, r := range layouts {
			narrowest, shortest = min(narrowest, r.Width), min(shortest, r.Height)
		}
		if narrowest >= config.DefaultWindowWidth && shortest >= config.DefaultWindowHeight {
			t.Errorf("%d panes on %dx%d leave the smallest at %dx%d, which the old minimum of %dx%d "+
				"could have satisfied; this size no longer demonstrates anything",
				c.n, c.w, c.h, narrowest, shortest, config.DefaultWindowWidth, config.DefaultWindowHeight)
		}
	}
}

// TestATightRegionGivesUpGroundBeforeItGivesUpTheRegion states the order of
// precedence when the asked-for gaps do not fit. Ground is spacing and a pane
// outside the region is not on screen, so the gaps shrink first.
//
// Without it, nine panes at a gap of two on a region six rows tall put the
// bottom row three rows past the end of it.
func TestATightRegionGivesUpGroundBeforeItGivesUpTheRegion(t *testing.T) {
	for _, c := range []struct{ n, w, h, gap int }{
		{9, 45, 6, 2}, {7, 30, 6, 2}, {9, 45, 8, 2}, {6, 24, 9, 3},
	} {
		layouts := CalculateTilingLayout(c.n, c.w, c.h, 0, 0.5, c.gap)
		for _, pair := range overlapping(layouts) {
			t.Errorf("n=%d %dx%d gap=%d: %+v and %+v overlap", c.n, c.w, c.h, c.gap, pair[0], pair[1])
		}
		for _, r := range outside(layouts, c.w, c.h, 0) {
			t.Errorf("n=%d %dx%d gap=%d: %+v is outside the region", c.n, c.w, c.h, c.gap, r)
		}
	}
}

// TestCalculateTilingLayout_WithTopMargin tests layout with top margin
func TestCalculateTilingLayout_WithTopMargin(t *testing.T) {
	topMargin := 2
	layouts := CalculateTilingLayout(2, 200, 100, topMargin, 0.5, 0)

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
	layouts := CalculateTilingLayout(0, 200, 100, 0, 0.5, 0)

	if layouts != nil {
		t.Errorf("Expected nil for 0 windows, got %d layouts", len(layouts))
	}
}

// BenchmarkCalculateTilingLayout benchmarks the tiling calculation
func BenchmarkCalculateTilingLayout(b *testing.B) {
	for b.Loop() {
		_ = CalculateTilingLayout(10, 1920, 1080, 0, 0.5, 0)
	}
}

// BenchmarkCalculateTilingLayout_ManyWindows benchmarks with many windows
func BenchmarkCalculateTilingLayout_ManyWindows(b *testing.B) {
	for b.Loop() {
		_ = CalculateTilingLayout(50, 1920, 1080, 0, 0.5, 0)
	}
}
