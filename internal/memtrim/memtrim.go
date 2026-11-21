// Package memtrim hands back to the operating system the memory a burst of
// work left behind: panes closing, or a flood of output that has ended.
//
// The Go runtime returns freed heap pages on its own, but only down to the
// heap goal the last collection set, and at idle there is no next collection
// to lower it. Without a trim, a daemon that had fifty panes, or that drew a
// few hundred megabytes of output, keeps that peak resident for as long as it
// lives: 121 MB of RSS against 13 MB of live heap after 49 of 50 panes closed.
//
// Request is cheap and safe to call from any goroutine, but it is meant for
// the edges of a burst (a pane closing, the client going idle, a periodic
// sweep), never for a hot path. It waits for things to settle and then trims
// only when the trim would give back enough to be worth a collection.
package memtrim

import (
	"runtime"
	"runtime/debug"
	"sync"
	"time"
)

const (
	settle       = 2 * time.Second
	minExcess    = 32 << 20
	maxBusyAlloc = 16 << 20
	minInterval  = 30 * time.Second
)

var (
	mu       sync.Mutex
	gen      uint64
	armAlloc uint64
	lastTrim time.Time
)

// Request asks for a trim once the process has been quiet for a moment.
// Calls during the wait push it back, so a burst of them is one look.
func Request() {
	allocs := read().allocs
	mu.Lock()
	armAlloc = allocs
	gen++
	g := gen
	mu.Unlock()
	time.AfterFunc(settle, func() { check(g) })
}

func check(g uint64) {
	mu.Lock()
	if g != gen {
		mu.Unlock()
		return
	}
	since := armAlloc
	recent := !lastTrim.IsZero() && time.Since(lastTrim) < minInterval
	mu.Unlock()
	if recent {
		return
	}
	s := read()
	if s.allocs-since > maxBusyAlloc || s.excess() < minExcess {
		return
	}
	debug.FreeOSMemory()
	mu.Lock()
	lastTrim = time.Now()
	mu.Unlock()
}

type sample struct {
	allocs, held, live uint64
}

func (s sample) excess() uint64 {
	if s.held <= s.live {
		return 0
	}
	return s.held - s.live
}

func read() sample {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return sample{
		allocs: ms.TotalAlloc,
		held:   ms.HeapInuse + ms.HeapIdle - ms.HeapReleased,
		live:   ms.NextGC / 2,
	}
}
