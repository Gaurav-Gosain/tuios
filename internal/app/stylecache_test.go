package app

import (
	"testing"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

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
