package app

import (
	"hash/maphash"
	"sync"
	"sync/atomic"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// StyleCache provides thread-safe caching of lipgloss styles with automatic eviction.
// It significantly reduces allocation pressure by reusing style objects for identical cell attributes.
type StyleCache struct {
	mu    sync.RWMutex
	cache map[uint64]lipgloss.Style
	seed  maphash.Seed

	// Statistics for monitoring (atomic counters)
	hits   atomic.Uint64
	misses atomic.Uint64
	evicts atomic.Uint64

	maxSize int // Maximum cache entries before eviction
}

// NewStyleCache creates a new style cache with the specified maximum size.
// Recommended size: 256-1024 entries (covers most terminal use cases).
func NewStyleCache(maxSize int) *StyleCache {
	if maxSize <= 0 {
		maxSize = 512 // Default size
	}
	return &StyleCache{
		cache:   make(map[uint64]lipgloss.Style, maxSize),
		seed:    maphash.MakeSeed(),
		maxSize: maxSize,
	}
}

// hashCellAttrs creates a hash key from cell attributes.
// The hash combines foreground color, background color, text attributes, and cursor state.
func (sc *StyleCache) hashCellAttrs(cell *uv.Cell, isCursor bool, isOptimized bool) uint64 {
	var h maphash.Hash
	h.SetSeed(sc.seed)

	// Hash cursor state (1 bit)
	if isCursor {
		_ = h.WriteByte(1)
	} else {
		_ = h.WriteByte(0)
	}

	// Hash optimized flag (1 bit)
	if isOptimized {
		_ = h.WriteByte(1)
	} else {
		_ = h.WriteByte(0)
	}

	if cell == nil {
		_ = h.WriteByte(0)
		return h.Sum64()
	}

	_ = h.WriteByte(1)

	// Hash text attributes (bold, italic, etc.)
	// Write as bytes to avoid alignment issues
	attrs := uint64(cell.Style.Attrs)
	_ = h.WriteByte(byte(attrs))
	_ = h.WriteByte(byte(attrs >> 8))
	_ = h.WriteByte(byte(attrs >> 16))
	_ = h.WriteByte(byte(attrs >> 24))
	_ = h.WriteByte(byte(attrs >> 32))
	_ = h.WriteByte(byte(attrs >> 40))
	_ = h.WriteByte(byte(attrs >> 48))
	_ = h.WriteByte(byte(attrs >> 56))

	// Hash foreground color
	if cell.Style.Fg != nil {
		if ansiColor, ok := cell.Style.Fg.(lipgloss.ANSIColor); ok {
			_ = h.WriteByte(1)
			_ = h.WriteByte(byte(ansiColor))
		} else {
			r, g, b, a := cell.Style.Fg.RGBA()
			_ = h.WriteByte(2)
			// Write RGBA values as bytes
			_ = h.WriteByte(byte(r >> 8))
			_ = h.WriteByte(byte(g >> 8))
			_ = h.WriteByte(byte(b >> 8))
			_ = h.WriteByte(byte(a >> 8))
		}
	} else {
		_ = h.WriteByte(0)
	}

	// Hash background color
	if cell.Style.Bg != nil {
		if ansiColor, ok := cell.Style.Bg.(lipgloss.ANSIColor); ok {
			_ = h.WriteByte(1)
			_ = h.WriteByte(byte(ansiColor))
		} else {
			r, g, b, a := cell.Style.Bg.RGBA()
			_ = h.WriteByte(2)
			// Write RGBA values as bytes
			_ = h.WriteByte(byte(r >> 8))
			_ = h.WriteByte(byte(g >> 8))
			_ = h.WriteByte(byte(b >> 8))
			_ = h.WriteByte(byte(a >> 8))
		}
	} else {
		_ = h.WriteByte(0)
	}

	return h.Sum64()
}

// Get retrieves a cached style or builds and caches it if not found.
// This is the main entry point for cached style access.
func (sc *StyleCache) Get(cell *uv.Cell, isCursor bool, optimized bool) lipgloss.Style {
	hash := sc.hashCellAttrs(cell, isCursor, optimized)

	// Fast path: try read lock first
	sc.mu.RLock()
	if style, ok := sc.cache[hash]; ok {
		sc.mu.RUnlock()
		sc.hits.Add(1)
		return style
	}
	sc.mu.RUnlock()

	// Cache miss: build style and cache it
	sc.misses.Add(1)

	var style lipgloss.Style
	if optimized {
		style = buildOptimizedCellStyle(cell)
	} else {
		style = buildCellStyle(cell, isCursor)
	}

	// Store in cache with write lock
	sc.mu.Lock()
	defer sc.mu.Unlock()

	// Check size and evict if necessary (simple LRU approximation: clear half the cache)
	if len(sc.cache) >= sc.maxSize {
		sc.evictHalf()
	}

	sc.cache[hash] = style
	return style
}

// evictHalf removes approximately half of the cache entries.
// This is a simple but effective eviction strategy that maintains good hit rates
// while preventing unbounded growth. Must be called with write lock held.
func (sc *StyleCache) evictHalf() {
	targetSize := sc.maxSize / 2
	evicted := 0

	// Delete entries until we reach target size
	// Note: map iteration order is randomized in Go, providing natural LRU-like behavior
	for key := range sc.cache {
		delete(sc.cache, key)
		evicted++
		if len(sc.cache) <= targetSize {
			break
		}
	}

	if evicted > 0 {
		sc.evicts.Add(uint64(evicted))
	}
}

// Clear removes all entries from the cache.
func (sc *StyleCache) Clear() {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	// Create new map instead of deleting entries (faster)
	sc.cache = make(map[uint64]lipgloss.Style, sc.maxSize)
	sc.evicts.Add(uint64(len(sc.cache)))
}

// StyleCacheStats holds cache statistics for monitoring and debugging.
type StyleCacheStats struct {
	Hits     uint64  // Number of cache hits
	Misses   uint64  // Number of cache misses
	Evicts   uint64  // Number of evicted entries
	Size     int     // Current cache size
	HitRate  float64 // Hit rate percentage (0-100)
	Capacity int     // Maximum cache capacity
}

// GetStats returns current cache statistics.
func (sc *StyleCache) GetStats() StyleCacheStats {
	sc.mu.RLock()
	size := len(sc.cache)
	sc.mu.RUnlock()

	hits := sc.hits.Load()
	misses := sc.misses.Load()
	evicts := sc.evicts.Load()

	total := hits + misses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(hits) / float64(total) * 100.0
	}

	return StyleCacheStats{
		Hits:     hits,
		Misses:   misses,
		Evicts:   evicts,
		Size:     size,
		HitRate:  hitRate,
		Capacity: sc.maxSize,
	}
}

// ResetStats resets all statistics counters to zero.
func (sc *StyleCache) ResetStats() {
	sc.hits.Store(0)
	sc.misses.Store(0)
	sc.evicts.Store(0)
}

// Global style cache instance
var globalStyleCache = NewStyleCache(1024)

// GetGlobalStyleCache returns the global style cache instance.
// This is used by the rendering functions to cache styles across all windows.
func GetGlobalStyleCache() *StyleCache {
	return globalStyleCache
}

// SetGlobalStyleCacheSize updates the maximum size of the global cache.
// This should be called during initialization, not during active rendering.
func SetGlobalStyleCacheSize(size int) {
	globalStyleCache.mu.Lock()
	defer globalStyleCache.mu.Unlock()

	globalStyleCache.maxSize = size
	if len(globalStyleCache.cache) > size {
		globalStyleCache.evictHalf()
	}
}
