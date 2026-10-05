// Package memtrim hands back to the operating system the memory a burst of
// work left behind: panes closing, or a flood of output that has ended.
//
// The Go runtime returns freed heap pages on its own, but only down to the
// heap goal the last collection set, and at idle there is no next collection
// to lower it. A daemon that had fifty panes, or that drew a few hundred
// megabytes of output, kept that peak resident for as long as it lived: 121 MB
// of RSS against 13 MB of live heap after 49 of 50 panes closed.
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
	// settle is how long Request waits after the last call before it looks.
	// A burst of closes is one trim at its end.
	settle = 2 * time.Second
	// minExcess is the least heap held beyond the live data that is worth a
	// forced collection to give back.
	minExcess = 32 << 20
	// maxBusyAlloc is the most a process may allocate during the settle
	// window and still count as quiet. A process allocating faster is in the
	// middle of a burst, and what it gave back it would take again at once.
	maxBusyAlloc = 16 << 20
	// minInterval is the least time between two trims.
	minInterval = 30 * time.Second
)

var (
	mu sync.Mutex
	// gen counts Requests. Each one starts its own timer, and only the timer
	// of the latest Request looks. A Request never stops or resets an
	// earlier timer: a timer started inside a synctest bubble belongs to that
	// bubble, and touching it from outside is a fatal runtime error.
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

// check runs on the timer's goroutine: it trims when the process was quiet
// over the wait and holds enough heap it no longer uses. A timer whose
// Request was followed by another one does nothing.
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

// sample is the part of the runtime's memory statistics a trim decision
// reads.
type sample struct {
	allocs, held, live uint64
}

// excess is the heap the process holds beyond its live data: objects that
// are garbage since the last collection, unused span space, and free pages
// not yet returned.
func (s sample) excess() uint64 {
	if s.held <= s.live {
		return 0
	}
	return s.held - s.live
}

// read takes the statistics. runtime/metrics would avoid the brief stop of
// ReadMemStats but costs about 28 KB of binary, and this runs only at the
// edges of a burst. The live heap is not in MemStats: the last collection
// set the next goal GOGC percent above it, and tuios leaves GOGC at 100, so
// it is about half the goal.
func read() sample {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return sample{
		allocs: ms.TotalAlloc,
		held:   ms.HeapInuse + ms.HeapIdle - ms.HeapReleased,
		live:   ms.NextGC / 2,
	}
}
