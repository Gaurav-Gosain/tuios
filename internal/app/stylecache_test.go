package app

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// TestStyleCacheBasic tests basic cache functionality
func TestStyleCacheBasic(t *testing.T) {
	cache := NewStyleCache(10)

	// Create a test cell
	cell := &uv.Cell{
		Content: "A",
		Style: uv.Style{
			Fg:    lipgloss.Color("15"),
			Bg:    lipgloss.Color("0"),
			Attrs: 1, // Bold
		},
	}

	// First access should be a miss
	style1 := cache.Get(cell, false, false)
	stats1 := cache.GetStats()
	if stats1.Hits != 0 {
		t.Errorf("Expected 0 hits, got %d", stats1.Hits)
	}
	if stats1.Misses != 1 {
		t.Errorf("Expected 1 miss, got %d", stats1.Misses)
	}

	// Second access with same cell should be a hit
	style2 := cache.Get(cell, false, false)
	stats2 := cache.GetStats()
	if stats2.Hits != 1 {
		t.Errorf("Expected 1 hit, got %d", stats2.Hits)
	}
	if stats2.Misses != 1 {
		t.Errorf("Expected 1 miss, got %d", stats2.Misses)
	}

	// Styles should render identically
	if style1.Render("test") != style2.Render("test") {
		t.Error("Cached style renders differently than original")
	}
}

// TestStyleCacheDifferentAttributes tests that different attributes create different cache entries
func TestStyleCacheDifferentAttributes(t *testing.T) {
	cache := NewStyleCache(10)

	cell1 := &uv.Cell{
		Style: uv.Style{
			Fg:    lipgloss.Color("15"),
			Attrs: 1, // Bold
		},
	}

	cell2 := &uv.Cell{
		Style: uv.Style{
			Fg:    lipgloss.Color("15"),
			Attrs: 4, // Italic
		},
	}

	// Get styles for both cells
	cache.Get(cell1, false, false)
	cache.Get(cell2, false, false)

	stats := cache.GetStats()
	if stats.Size != 2 {
		t.Errorf("Expected 2 cache entries, got %d", stats.Size)
	}
	if stats.Misses != 2 {
		t.Errorf("Expected 2 misses (different attributes), got %d", stats.Misses)
	}
}

// TestStyleCacheCursorDifference tests that cursor state creates different cache entries
func TestStyleCacheCursorDifference(t *testing.T) {
	cache := NewStyleCache(10)

	cell := &uv.Cell{
		Style: uv.Style{
			Fg: lipgloss.Color("15"),
		},
	}

	// Get style without cursor
	cache.Get(cell, false, false)
	// Get style with cursor
	cache.Get(cell, true, false)

	stats := cache.GetStats()
	if stats.Size != 2 {
		t.Errorf("Expected 2 cache entries (cursor vs no cursor), got %d", stats.Size)
	}
}

// TestStyleCacheOptimizedMode tests that optimized mode creates separate entries
func TestStyleCacheOptimizedMode(t *testing.T) {
	cache := NewStyleCache(10)

	cell := &uv.Cell{
		Style: uv.Style{
			Fg:    lipgloss.Color("15"),
			Attrs: 1, // Bold
		},
	}

	// Get style in normal mode
	cache.Get(cell, false, false)
	// Get style in optimized mode (should skip attributes)
	cache.Get(cell, false, true)

	stats := cache.GetStats()
	if stats.Size != 2 {
		t.Errorf("Expected 2 cache entries (normal vs optimized), got %d", stats.Size)
	}
}

// TestStyleCacheEviction tests that cache evicts entries when full
func TestStyleCacheEviction(t *testing.T) {
	cache := NewStyleCache(10) // Small cache for testing

	// Fill cache beyond capacity with truly unique cells
	// We need to vary multiple attributes to ensure unique hashes
	for i := range 30 {
		// Create unique cells by varying both color and cursor state
		cell := &uv.Cell{
			Style: uv.Style{
				Fg:    lipgloss.ANSIColor(uint8(i)), // Different color for each
				Attrs: uint8(i % 16),                // Different attributes
			},
		}
		// Alternate cursor state to create even more unique entries
		isCursor := i%2 == 0
		cache.Get(cell, isCursor, false)
	}

	stats := cache.GetStats()
	// Cache should never exceed max size
	if stats.Size > 10 {
		t.Errorf("Cache size exceeded max: %d > 10", stats.Size)
	}
	// With 30 unique entries and max size 10, evictions must have occurred
	if stats.Evicts == 0 {
		t.Error("Expected evictions to occur with 30 unique entries")
	}
	// Cache size should be reasonable (between 5 and 10 after evictions)
	if stats.Size < 5 || stats.Size > 10 {
		t.Errorf("Cache size outside expected range: %d (expected 5-10)", stats.Size)
	}
}

// TestStyleCacheHitRate tests that hit rate is calculated correctly
func TestStyleCacheHitRate(t *testing.T) {
	cache := NewStyleCache(10)

	cell := &uv.Cell{
		Style: uv.Style{
			Fg: lipgloss.Color("15"),
		},
	}

	// First access: miss
	cache.Get(cell, false, false)
	// Next 9 accesses: hits
	for range 9 {
		cache.Get(cell, false, false)
	}

	stats := cache.GetStats()
	expectedHitRate := 90.0 // 9 hits out of 10 total
	if stats.HitRate < expectedHitRate-0.1 || stats.HitRate > expectedHitRate+0.1 {
		t.Errorf("Expected hit rate ~%.2f%%, got %.2f%%", expectedHitRate, stats.HitRate)
	}
}

// TestStyleCacheClear tests that Clear removes all entries
func TestStyleCacheClear(t *testing.T) {
	cache := NewStyleCache(10)

	// Add some entries
	for i := range 5 {
		cell := &uv.Cell{
			Style: uv.Style{
				Attrs: uint8(i),
			},
		}
		cache.Get(cell, false, false)
	}

	// Clear cache
	cache.Clear()

	stats := cache.GetStats()
	if stats.Size != 0 {
		t.Errorf("Expected cache size 0 after clear, got %d", stats.Size)
	}
}

// TestStyleCacheResetStats tests that ResetStats clears counters
func TestStyleCacheResetStats(t *testing.T) {
	cache := NewStyleCache(10)

	cell := &uv.Cell{
		Style: uv.Style{
			Fg: lipgloss.Color("15"),
		},
	}

	// Generate some statistics
	cache.Get(cell, false, false) // Miss
	cache.Get(cell, false, false) // Hit
	cache.Get(cell, false, false) // Hit

	// Reset stats
	cache.ResetStats()

	stats := cache.GetStats()
	if stats.Hits != 0 {
		t.Errorf("Expected 0 hits after reset, got %d", stats.Hits)
	}
	if stats.Misses != 0 {
		t.Errorf("Expected 0 misses after reset, got %d", stats.Misses)
	}
	// Cache entries should remain (size >= 1)
	if stats.Size < 1 {
		t.Error("Cache entries should not be cleared by ResetStats")
	}
}

// TestStyleCacheNilCell tests that nil cells are handled correctly
func TestStyleCacheNilCell(t *testing.T) {
	cache := NewStyleCache(10)

	// Nil cell should return empty style
	style := cache.Get(nil, false, false)
	if style.Render("test") == "" {
		t.Error("Style should still render content even for nil cell")
	}

	// Multiple nil accesses should hit cache
	cache.Get(nil, false, false)
	cache.Get(nil, false, false)

	stats := cache.GetStats()
	if stats.Hits < 2 {
		t.Errorf("Expected at least 2 hits for nil cells, got %d", stats.Hits)
	}
}

// TestStyleCacheColorTypes tests different color types (ANSI vs RGB)
func TestStyleCacheColorTypes(t *testing.T) {
	cache := NewStyleCache(10)

	// ANSI color cell
	cell1 := &uv.Cell{
		Style: uv.Style{
			Fg: lipgloss.ANSIColor(15),
		},
	}

	// RGB color cell with same visual color
	cell2 := &uv.Cell{
		Style: uv.Style{
			Fg: color.RGBA{R: 255, G: 255, B: 255, A: 255},
		},
	}

	cache.Get(cell1, false, false)
	cache.Get(cell2, false, false)

	stats := cache.GetStats()
	// Different color types should create different cache entries
	if stats.Size < 2 {
		t.Errorf("Expected separate entries for ANSI vs RGB colors, got size %d", stats.Size)
	}
}

// BenchmarkStyleCacheHit benchmarks cache hit performance
func BenchmarkStyleCacheHit(b *testing.B) {
	cache := NewStyleCache(1024)

	cell := &uv.Cell{
		Style: uv.Style{
			Fg:    lipgloss.Color("15"),
			Bg:    lipgloss.Color("0"),
			Attrs: 1,
		},
	}

	// Prime the cache
	cache.Get(cell, false, false)

	b.ResetTimer()
	for b.Loop() {
		cache.Get(cell, false, false)
	}
}

// BenchmarkStyleCacheMiss benchmarks cache miss (new entry) performance
func BenchmarkStyleCacheMiss(b *testing.B) {
	cache := NewStyleCache(1024)

	i := 0
	b.ResetTimer()
	for b.Loop() {
		cell := &uv.Cell{
			Style: uv.Style{
				Fg:    lipgloss.Color("15"),
				Attrs: uint8(1 << uint(i%10)), // Vary attributes to force misses
			},
		}
		cache.Get(cell, false, false)
		i++
	}
}

// BenchmarkStyleNoCacheBaseline benchmarks style creation without caching
func BenchmarkStyleNoCacheBaseline(b *testing.B) {
	cell := &uv.Cell{
		Style: uv.Style{
			Fg:    lipgloss.Color("15"),
			Bg:    lipgloss.Color("0"),
			Attrs: 1,
		},
	}

	b.ResetTimer()
	for b.Loop() {
		buildCellStyle(cell, false)
	}
}
