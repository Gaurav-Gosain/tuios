package vt

import (
	"sync"
	"testing"
)

// TestScrollbackConcurrentLine reproduces the race that took the daemon down:
// two or more capturers calling Line() at once, which is what two overlapping
// `wait-for window-output` calls do. Line() decodes and caches into the
// sb.cache map, and without a lock two readers write it at the same time and
// the Go runtime aborts the process with "concurrent map writes".
//
// With cacheMu it runs clean under -race.
func TestScrollbackConcurrentLine(t *testing.T) {
	sb := NewScrollback(10000)
	for i := 0; i < 3000; i++ {
		sb.PushBlankLine(80)
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < sb.Len(); i++ {
				_ = sb.Line(i)
			}
		}()
	}
	wg.Wait()
}
