package app

import (
	"hash/maphash"
	"image/color"
	"sync"
	"sync/atomic"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

type styleEntry struct {
	style  lipgloss.Style
	prefix string
	suffix string
}

// StyleCache provides thread-safe caching of lipgloss styles with automatic eviction.
// It significantly reduces allocation pressure by reusing style objects for identical cell attributes.
type StyleCache struct {
	mu    sync.RWMutex
	cache map[uint64]styleEntry
	seed  maphash.Seed

	hits   atomic.Uint64
	misses atomic.Uint64
	evicts atomic.Uint64

	maxSize int
}

// NewStyleCache creates a new style cache with the specified maximum size.
// Recommended size: 256-1024 entries (covers most terminal use cases).
func NewStyleCache(maxSize int) *StyleCache {
	if maxSize <= 0 {
		maxSize = 512
	}
	return &StyleCache{
		cache:   make(map[uint64]styleEntry, maxSize),
		seed:    maphash.MakeSeed(),
		maxSize: maxSize,
	}
}

func hashColor(h *maphash.Hash, c color.Color) {
	if ansiColor, ok := c.(lipgloss.ANSIColor); ok {
		_ = h.WriteByte(1)
		_ = h.WriteByte(byte(ansiColor))
		return
	}
	if c == nil || isNilColor(c) {
		_ = h.WriteByte(0)
		return
	}
	r, g, b, a := c.RGBA()
	_ = h.WriteByte(2)
	_ = h.WriteByte(byte(r >> 8))
	_ = h.WriteByte(byte(g >> 8))
	_ = h.WriteByte(byte(b >> 8))
	_ = h.WriteByte(byte(a >> 8))
}

func (sc *StyleCache) hashCellAttrs(cell *uv.Cell, isCursor bool) uint64 {
	var h maphash.Hash
	h.SetSeed(sc.seed)

	if isCursor {
		_ = h.WriteByte(1)
	} else {
		_ = h.WriteByte(0)
	}

	if cell == nil {
		_ = h.WriteByte(0)
		return h.Sum64()
	}

	_ = h.WriteByte(1)

	_ = h.WriteByte(cell.Style.Attrs)
	_ = h.WriteByte(byte(cell.Style.Underline))

	hashColor(&h, cell.Style.Fg)
	hashColor(&h, cell.Style.Bg)
	if cell.Style.UnderlineColor == nil {
		_ = h.WriteByte(0)
	} else {
		hashColor(&h, cell.Style.UnderlineColor)
	}

	return h.Sum64()
}

func (sc *StyleCache) getEntry(cell *uv.Cell, isCursor bool) styleEntry {
	hash := sc.hashCellAttrs(cell, isCursor)

	sc.mu.RLock()
	if entry, ok := sc.cache[hash]; ok {
		sc.mu.RUnlock()
		sc.hits.Add(1)
		return entry
	}
	sc.mu.RUnlock()

	sc.misses.Add(1)

	style := buildCellStyle(cell, isCursor)
	prefix, suffix := styleToANSI(style)
	entry := styleEntry{style: style, prefix: prefix, suffix: suffix}

	sc.mu.Lock()
	defer sc.mu.Unlock()

	if len(sc.cache) >= sc.maxSize {
		sc.evictHalf()
	}

	sc.cache[hash] = entry
	return entry
}

// GetWithANSI retrieves a cached style together with its derived ANSI escape
// prefix and suffix, building and caching the entry on a miss. The escape is a
// pure function of the style, so the render loop can emit the cached prefix and
// suffix directly instead of re-deriving them via styleToANSI on every flush.
func (sc *StyleCache) GetWithANSI(cell *uv.Cell, isCursor bool) (lipgloss.Style, string, string) {
	entry := sc.getEntry(cell, isCursor)
	return entry.style, entry.prefix, entry.suffix
}

func (sc *StyleCache) evictHalf() {
	targetSize := sc.maxSize / 2
	evicted := 0

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

var globalStyleCache = NewStyleCache(1024)

// GetGlobalStyleCache returns the global style cache instance.
// This is used by the rendering functions to cache styles across all windows.
func GetGlobalStyleCache() *StyleCache {
	return globalStyleCache
}
