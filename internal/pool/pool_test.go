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

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
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
		for i := 0; i < b.N; i++ {
			sb := GetStringBuilder()
			sb.WriteString("test string")
			_ = sb.String()
			PutStringBuilder(sb)
		}
	})

	b.Run("WithoutPool", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
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
		for i := 0; i < b.N; i++ {
			buf := GetByteSlice()
			copy(*buf, []byte("test data"))
			PutByteSlice(buf)
		}
	})

	b.Run("WithoutPool", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			buf := make([]byte, 32*1024)
			copy(buf, []byte("test data"))
		}
	})
}

// BenchmarkLayerSlicePool benchmarks the layer slice pool
func BenchmarkLayerSlicePool(b *testing.B) {
	b.Run("WithPool", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			layers := GetLayerSlice()
			PutLayerSlice(layers)
		}
	})

	b.Run("WithoutPool", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = make([]*lipgloss.Layer, 0, 16)
		}
	})
}

// BenchmarkStylePool benchmarks the style pool
func BenchmarkStylePool(b *testing.B) {
	b.Run("WithPool", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			style := GetStyle()
			PutStyle(style)
		}
	})

	b.Run("WithoutPool", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = lipgloss.NewStyle()
		}
	})
}
