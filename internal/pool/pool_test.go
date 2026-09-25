package pool

import (
	"testing"

	"charm.land/lipgloss/v2"
)

// BenchmarkByteSlicePool benchmarks the byte slice pool
func BenchmarkByteSlicePool(b *testing.B) {
	b.Run("WithPool", func(b *testing.B) {
		for b.Loop() {
			buf := GetByteSlice()
			copy(*buf, []byte("test data"))
			PutByteSlice(buf)
		}
	})

	b.Run("WithoutPool", func(b *testing.B) {
		for b.Loop() {
			buf := make([]byte, 32*1024)
			copy(buf, []byte("test data"))
		}
	})
}

// BenchmarkLayerSlicePool benchmarks the layer slice pool
func BenchmarkLayerSlicePool(b *testing.B) {
	b.Run("WithPool", func(b *testing.B) {
		for b.Loop() {
			layers := GetLayerSlice()
			PutLayerSlice(layers)
		}
	})

	b.Run("WithoutPool", func(b *testing.B) {
		for b.Loop() {
			_ = make([]*lipgloss.Layer, 0, 16)
		}
	})
}

// =============================================================================
// HighlightGrid Tests
// =============================================================================

// TestHighlightGrid_BoundsChecking tests bounds checking in HighlightGrid
func TestHighlightGrid_BoundsChecking(t *testing.T) {
	grid := GetHighlightGrid()
	grid.Init(10, 20)

	// Test out of bounds Set (should not panic)
	grid.Set(-1, 5) // Negative Y
	grid.Set(5, -1) // Negative X
	grid.Set(15, 5) // Y too large
	grid.Set(5, 25) // X too large

	// Test out of bounds Get (should return false)
	if grid.Get(-1, 5) {
		t.Error("Expected false for negative Y")
	}
	if grid.Get(5, -1) {
		t.Error("Expected false for negative X")
	}
	if grid.Get(15, 5) {
		t.Error("Expected false for Y too large")
	}
	if grid.Get(5, 25) {
		t.Error("Expected false for X too large")
	}

	// Test out of bounds HasRow
	if grid.HasRow(-1) {
		t.Error("Expected false for negative row")
	}
	if grid.HasRow(15) {
		t.Error("Expected false for row too large")
	}

	PutHighlightGrid(grid)
}

// TestHighlightGrid_Reuse tests that grids can be reused from the pool
func TestHighlightGrid_Reuse(t *testing.T) {
	// Get and initialize
	grid1 := GetHighlightGrid()
	grid1.Init(5, 5)
	grid1.Set(2, 2)
	PutHighlightGrid(grid1)

	// Get again: it should be reset
	grid2 := GetHighlightGrid()
	grid2.Init(10, 10)

	// The old value should not be present
	if grid2.Get(2, 2) {
		t.Error("Grid should be reset after returning to pool")
	}

	PutHighlightGrid(grid2)
}

// BenchmarkHighlightGrid benchmarks the highlight grid pool
func BenchmarkHighlightGrid(b *testing.B) {
	b.Run("WithPool", func(b *testing.B) {
		for b.Loop() {
			grid := GetHighlightGrid()
			grid.Init(50, 100)
			grid.Set(25, 50)
			_ = grid.Get(25, 50)
			PutHighlightGrid(grid)
		}
	})

	b.Run("WithoutPool", func(b *testing.B) {
		for b.Loop() {
			grid := &HighlightGrid{}
			grid.Init(50, 100)
			grid.Set(25, 50)
			_ = grid.Get(25, 50)
		}
	})
}
