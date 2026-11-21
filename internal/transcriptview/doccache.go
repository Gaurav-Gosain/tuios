package transcriptview

import (
	"sync"
	"time"
)

type docCache struct {
	mu      sync.Mutex
	entries []*docEntry
}

type docKey struct {
	path    string
	size    int64
	mod     time.Time
	leaf    string
	hasLeaf bool
}

type docEntry struct {
	key   docKey
	lines []byte
	used  time.Time
	refs  int
	dead  bool
}

const (
	docCacheEntries = 2
	docCacheMax     = 8 << 20
	docCacheTTL     = 2 * time.Minute
)

var docs docCache

func (c *docCache) get(key docKey) (*docEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.expireLocked(now)
	for _, e := range c.entries {
		if e.key == key {
			e.used = now
			e.refs++
			return e, true
		}
	}
	return nil, false
}

func (c *docCache) put(key docKey, lines []byte) *docEntry {
	if len(lines) > docCacheMax {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.expireLocked(now)
	for i, e := range c.entries {
		if e.key.path == key.path {
			c.dropLocked(e)
			c.entries = append(c.entries[:i], c.entries[i+1:]...)
			break
		}
	}
	if len(c.entries) == docCacheEntries {
		c.dropLocked(c.entries[0])
		c.entries = c.entries[1:]
	}
	e := &docEntry{key: key, lines: lines, used: now, refs: 1}
	c.entries = append(c.entries, e)
	return e
}

func (c *docCache) release(e *docEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e.refs--
	if e.dead && e.refs == 0 {
		clear(e.lines)
	}
}

func (c *docCache) dropLocked(e *docEntry) {
	e.dead = true
	if e.refs == 0 {
		clear(e.lines)
	}
}

func (c *docCache) expireLocked(now time.Time) {
	n := 0
	for _, e := range c.entries {
		if e.refs == 0 && now.Sub(e.used) > docCacheTTL {
			c.dropLocked(e)
			continue
		}
		c.entries[n] = e
		n++
	}
	clear(c.entries[n:])
	c.entries = c.entries[:n]
}
