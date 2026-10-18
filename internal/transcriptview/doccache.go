package transcriptview

import (
	"sync"
	"time"
)

// docCache keeps the records made from a document format for a short time.
// Amp rewrites a thread of several megabytes whole, and a person reading it
// pages through it, so without the cache each page would read and parse the
// whole file again.
//
// It is the one place this package keeps what it read, so it is held small:
// docCacheEntries documents of at most docCacheMax bytes each, for
// docCacheTTL after their last use, zeroed when they go. A document still in
// use by a read is zeroed when that read releases it. The key is the path,
// size and modification time, so a rewritten file is read again.
type docCache struct {
	mu      sync.Mutex
	entries []*docEntry
}

type docKey struct {
	path string
	size int64
	mod  time.Time
}

type docEntry struct {
	key   docKey
	lines []byte
	used  time.Time
	// refs counts the reads using lines, and dead says the entry left the
	// cache, so the last release zeroes it.
	refs int
	dead bool
}

const (
	docCacheEntries = 2
	docCacheMax     = 8 << 20
	docCacheTTL     = 2 * time.Minute
)

var docs docCache

// get returns the entry cached for key, held until release.
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

// put keeps lines for key and returns the entry, held until release, or nil
// when the document is past docCacheMax and the caller keeps it.
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
			// The same file, older: the new records replace it.
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

// release ends a read's use of e.
func (c *docCache) release(e *docEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e.refs--
	if e.dead && e.refs == 0 {
		clear(e.lines)
	}
}

// dropLocked takes e out of use: it is zeroed now, or by its last release.
func (c *docCache) dropLocked(e *docEntry) {
	e.dead = true
	if e.refs == 0 {
		clear(e.lines)
	}
}

// expireLocked drops the entries not used for docCacheTTL.
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
