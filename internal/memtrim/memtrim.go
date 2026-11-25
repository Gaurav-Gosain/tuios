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
	"runtime/metrics"
	"sync"
	"time"
)

const (
	// settle is how long Request waits after the last call before it looks.
	// A burst of closes is one trim at its end.
	settle = 2 * time.Second
	// minExcess is the least a trim must be able to give back to be worth a
	// forced collection. An idle client with three shells holds 5 to 9 MB of
	// garbage and free pages past its live heap, and a daemon about as much:
	// at 32 MB the trim never ran outside a pane flood. At 4 MB the trim of
	// what the start leaves sat at the threshold: the daemon holds 4.1 to
	// 4.7 MB then. After a trim the excess is under 0.3 MB, so 2 MB does not
	// trim twice for the same garbage.
	minExcess = 2 << 20
	// liveShare is the part of the live heap a trim must also give back: the
	// collection marks the whole live heap, so a large heap trims only for
	// garbage in proportion to it, and not for the noise of a few panes.
	liveShare = 8
	// maxBusyAlloc is the most a process may allocate during the settle
	// window and still count as quiet. A process allocating faster is in the
	// middle of a burst, and what it gave back it would take again at once.
	// An idle client allocates about 15 KB in the window, and a client
	// drawing a pane that prints 300 lines ten times a second about 1.2 MB.
	maxBusyAlloc = 512 << 10
	// minInterval is the least time between two trims. A look that comes
	// sooner waits for the interval to end. A daemon asks only while its
	// cleanup runs, and those stop soon after the output does, so a look
	// that gave up would leave the burst untrimmed.
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
	var wait time.Duration
	if !lastTrim.IsZero() {
		wait = minInterval - time.Since(lastTrim)
	}
	mu.Unlock()
	if wait > 0 {
		time.AfterFunc(wait, func() { rearm(g) })
		return
	}
	s := read()
	if s.allocs-since > maxBusyAlloc || s.excess() < max(minExcess, s.live/liveShare) {
		return
	}
	debug.FreeOSMemory()
	mu.Lock()
	lastTrim = time.Now()
	mu.Unlock()
}

// rearm starts the settle again once the minimum interval has passed, as a
// new Request would, so the process must be quiet over the settle before the
// trim and not over the whole interval.
func rearm(g uint64) {
	allocs := read().allocs
	mu.Lock()
	if g != gen {
		mu.Unlock()
		return
	}
	armAlloc = allocs
	mu.Unlock()
	time.AfterFunc(settle, func() { check(g) })
}

// sample is the part of the runtime's memory statistics a trim decision
// reads.
type sample struct {
	allocs, alloc, live, free uint64
}

// excess is what a trim can give back: the objects allocated since the last
// collection that are not part of the live data, and the free pages not yet
// returned. Free space inside spans that still hold live objects is left
// out. A collection keeps those spans, so counting them would trim again at
// every settle and give back nothing.
func (s sample) excess() uint64 {
	var garbage uint64
	if s.alloc > s.live {
		garbage = s.alloc - s.live
	}
	return garbage + s.free
}

// read takes the statistics. The live heap is the one the last collection
// marked. Half the heap goal is above it by half the stacks and globals,
// about 0.45 MB in an idle daemon, and with GOGC=off the goal says nothing.
func read() sample {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	live := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	metrics.Read(live)
	return sample{
		allocs: ms.TotalAlloc,
		alloc:  ms.HeapAlloc,
		live:   live[0].Value.Uint64(),
		free:   ms.HeapIdle - ms.HeapReleased,
	}
}
