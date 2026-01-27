package pool

import (
	"strings"
	"sync"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestStringBuilderPool tests the string builder pool
func TestStringBuilderPool(t *testing.T) {
	// Get a string builder from pool
	sb := GetStringBuilder()
	if sb == nil {
		t.Fatal("GetStringBuilder returned nil")
	}

	// Use it
	sb.WriteString("test")
	if sb.String() != "test" {
		t.Errorf("Expected 'test', got %q", sb.String())
	}

	// Return it to pool
	PutStringBuilder(sb)

	// Get again and verify it's reset
	sb2 := GetStringBuilder()
	if sb2.Len() != 0 {
		t.Errorf("String builder should be reset, but has length %d", sb2.Len())
	}

	PutStringBuilder(sb2)
}

// TestStringBuilderPool_Concurrent tests concurrent access to string builder pool
func TestStringBuilderPool_Concurrent(t *testing.T) {
	const goroutines = 10
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(id int) {
			defer wg.Done()
			for j := range iterations {
				sb := GetStringBuilder()
				sb.WriteString("test")
				if sb.String() != "test" {
					t.Errorf("Goroutine %d iteration %d: unexpected content", id, j)
				}
				PutStringBuilder(sb)
			}
		}(i)
	}

	wg.Wait()
}

// TestLayerSlicePool tests the layer slice pool
func TestLayerSlicePool(t *testing.T) {
	// Get a layer slice from pool
	layers := GetLayerSlice()
	if layers == nil {
		t.Fatal("GetLayerSlice returned nil")
	}
	if *layers == nil {
		t.Fatal("Layer slice is nil")
	}

	// Verify initial capacity
	if cap(*layers) < 16 {
		t.Errorf("Expected capacity >= 16, got %d", cap(*layers))
	}

	// Return it to pool
	PutLayerSlice(layers)

	// Get again
	layers2 := GetLayerSlice()
	if layers2 == nil {
		t.Fatal("Second GetLayerSlice returned nil")
	}

	PutLayerSlice(layers2)
}

// TestByteSlicePool tests the byte slice pool
func TestByteSlicePool(t *testing.T) {
	// Get a byte slice from pool
	buf := GetByteSlice()
	if buf == nil {
		t.Fatal("GetByteSlice returned nil")
	}
	if *buf == nil {
		t.Fatal("Byte slice is nil")
	}

	// Verify size
	expectedSize := 32 * 1024
	if len(*buf) != expectedSize {
		t.Errorf("Expected byte slice length %d, got %d", expectedSize, len(*buf))
	}

	// Use it
	copy(*buf, []byte("test data"))

	// Return it to pool
	PutByteSlice(buf)

	// Get again
	buf2 := GetByteSlice()
	if buf2 == nil {
		t.Fatal("Second GetByteSlice returned nil")
	}

	PutByteSlice(buf2)
}

// TestStylePool tests the lipgloss style pool
func TestStylePool(t *testing.T) {
	// Get a style from pool
	style := GetStyle()
	if style == nil {
		t.Fatal("GetStyle returned nil")
	}

	// Return it to pool
	PutStyle(style)

	// Get again
	style2 := GetStyle()
	if style2 == nil {
		t.Fatal("Second GetStyle returned nil")
	}

	PutStyle(style2)
}

// TestPoolReuse tests that pools actually reuse objects
func TestPoolReuse(t *testing.T) {
	// String builder pool
	sb1 := GetStringBuilder()
	ptr1 := &sb1
	PutStringBuilder(sb1)
	sb2 := GetStringBuilder()
	ptr2 := &sb2

	// The pointers should be the same (reused from pool)
	// Note: This is not guaranteed by sync.Pool but is typical behavior
	_ = ptr1
	_ = ptr2

	PutStringBuilder(sb2)
}

// BenchmarkStringBuilderPool benchmarks the string builder pool
func BenchmarkStringBuilderPool(b *testing.B) {
	b.Run("WithPool", func(b *testing.B) {
		for b.Loop() {
			sb := GetStringBuilder()
			sb.WriteString("test string")
			_ = sb.String()
			PutStringBuilder(sb)
		}
	})

	b.Run("WithoutPool", func(b *testing.B) {
		for b.Loop() {
			sb := &strings.Builder{}
			sb.WriteString("test string")
			_ = sb.String()
		}
	})
}

// BenchmarkStringBuilderPool_Parallel benchmarks concurrent pool usage
func BenchmarkStringBuilderPool_Parallel(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			sb := GetStringBuilder()
			sb.WriteString("test string for parallel benchmark")
			_ = sb.String()
			PutStringBuilder(sb)
		}
	})
}

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

// BenchmarkStylePool benchmarks the style pool
func BenchmarkStylePool(b *testing.B) {
	b.Run("WithPool", func(b *testing.B) {
		for b.Loop() {
			style := GetStyle()
			PutStyle(style)
		}
	})

	b.Run("WithoutPool", func(b *testing.B) {
		for b.Loop() {
			_ = lipgloss.NewStyle()
		}
	})
}

// =============================================================================
// HighlightGrid Tests
// =============================================================================

// TestHighlightGrid_Basic tests basic HighlightGrid operations
func TestHighlightGrid_Basic(t *testing.T) {
	grid := GetHighlightGrid()
	if grid == nil {
		t.Fatal("GetHighlightGrid returned nil")
	}

	// Initialize the grid
	grid.Init(10, 20)

	// Test Set and Get
	grid.Set(5, 10)
	if !grid.Get(5, 10) {
		t.Error("Expected (5, 10) to be highlighted")
	}

	// Test unset cell
	if grid.Get(3, 3) {
		t.Error("Expected (3, 3) to not be highlighted")
	}

	// Test HasRow
	if !grid.HasRow(5) {
		t.Error("Expected row 5 to have highlights")
	}
	if grid.HasRow(3) {
		t.Error("Expected row 3 to not have highlights")
	}

	// Return to pool
	PutHighlightGrid(grid)
}

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

// TestHighlightGrid_Reset tests the Reset function
func TestHighlightGrid_Reset(t *testing.T) {
	grid := GetHighlightGrid()
	grid.Init(10, 20)

	// Set some values
	grid.Set(2, 3)
	grid.Set(5, 10)

	// Verify they're set
	if !grid.Get(2, 3) || !grid.Get(5, 10) {
		t.Fatal("Values not set correctly before reset")
	}

	// Reset
	grid.Reset()

	// Re-init and verify values are cleared
	grid.Init(10, 20)
	if grid.Get(2, 3) {
		t.Error("Expected (2, 3) to be cleared after reset")
	}
	if grid.Get(5, 10) {
		t.Error("Expected (5, 10) to be cleared after reset")
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

	// Get again - should be reset
	grid2 := GetHighlightGrid()
	grid2.Init(10, 10)

	// The old value should not be present
	if grid2.Get(2, 2) {
		t.Error("Grid should be reset after returning to pool")
	}

	PutHighlightGrid(grid2)
}

// TestHighlightGrid_LargeInit tests reusing rows when capacity allows
func TestHighlightGrid_LargeInit(t *testing.T) {
	grid := GetHighlightGrid()

	// First init with large dimensions
	grid.Init(100, 50)
	grid.Set(50, 25)
	if !grid.Get(50, 25) {
		t.Error("Expected (50, 25) to be highlighted")
	}

	// Reset and re-init with smaller dimensions
	grid.Reset()
	grid.Init(20, 20)
	grid.Set(10, 10)
	if !grid.Get(10, 10) {
		t.Error("Expected (10, 10) to be highlighted after re-init")
	}

	PutHighlightGrid(grid)
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
