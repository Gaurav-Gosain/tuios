package app

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

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
	cache.Get(cell1, false)
	cache.Get(cell2, false)

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
	cache.Get(cell, false)
	// Get style with cursor
	cache.Get(cell, true)

	stats := cache.GetStats()
	if stats.Size != 2 {
		t.Errorf("Expected 2 cache entries (cursor vs no cursor), got %d", stats.Size)
	}
}

// TestStyleCacheKeysOnUnderline checks that the underline style and colour are
// part of the cache key. Cells that differ only there must not share an entry,
// or the second would be drawn with the first one's underline.
func TestStyleCacheKeysOnUnderline(t *testing.T) {
	cache := NewStyleCache(10)

	plain := &uv.Cell{Style: uv.Style{Fg: lipgloss.Color("15")}}
	single := &uv.Cell{Style: uv.Style{Fg: lipgloss.Color("15"), Underline: uv.UnderlineSingle}}
	curly := &uv.Cell{Style: uv.Style{Fg: lipgloss.Color("15"), Underline: uv.UnderlineCurly}}
	coloured := &uv.Cell{Style: uv.Style{
		Fg: lipgloss.Color("15"), Underline: uv.UnderlineCurly, UnderlineColor: lipgloss.Color("9"),
	}}

	prefixes := map[string]bool{}
	for _, cell := range []*uv.Cell{plain, single, curly, coloured} {
		_, prefix, _ := cache.GetWithANSI(cell, false)
		prefixes[prefix] = true
	}
	if stats := cache.GetStats(); stats.Size != 4 {
		t.Errorf("expected 4 cache entries, got %d", stats.Size)
	}
	if len(prefixes) != 4 {
		t.Errorf("expected 4 distinct escapes, got %v", prefixes)
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
		cache.Get(cell, isCursor)
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

// TestStyleCacheNilCell tests that nil cells are handled correctly
func TestStyleCacheNilCell(t *testing.T) {
	cache := NewStyleCache(10)

	// Nil cell should return empty style
	style := cache.Get(nil, false)
	if style.Render("test") == "" {
		t.Error("Style should still render content even for nil cell")
	}

	// Multiple nil accesses should hit cache
	cache.Get(nil, false)
	cache.Get(nil, false)

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

	cache.Get(cell1, false)
	cache.Get(cell2, false)

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
	cache.Get(cell, false)

	b.ResetTimer()
	for b.Loop() {
		cache.Get(cell, false)
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
		cache.Get(cell, false)
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
