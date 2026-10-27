package vt

import (
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// TestReleaseScrollbackCacheDropsTheLines: reading a scrolled screen leaves
// its decoded history lines in the scrollback's cache, and nothing reads them
// again once the pane is back at the bottom. The release drops them. Lines a
// reader already holds stay as they were, and the next read decodes the same
// lines again.
func TestReleaseScrollbackCacheDropsTheLines(t *testing.T) {
	const (
		cols  = 207
		rows  = 55
		lines = 3000
		read  = 100
	)
	filled := func() *Emulator {
		e := NewEmulator(cols, rows)
		for i := range lines {
			_, _ = fmt.Fprintf(e, "history line %d of %d\r\n", i, lines)
		}
		return e
	}
	cached := func(sb *Scrollback) (entries, cells int) {
		sb.cacheMu.Lock()
		defer sb.cacheMu.Unlock()
		return len(sb.cache), sb.cacheCells
	}
	// readLines reads the first lines of the history and returns them with
	// a copy of each.
	readLines := func(e *Emulator) (kept, copies []uv.Line) {
		for i := range read {
			line := e.ScrollbackLine(i)
			if line == nil {
				t.Fatalf("history line %d is missing", i)
			}
			kept = append(kept, line)
			copies = append(copies, slices.Clone(line))
		}
		return kept, copies
	}

	t.Run("a scrolled pane", func(t *testing.T) {
		e := filled()
		sb := e.Scrollback()
		kept, copies := readLines(e)
		if n, cells := cached(sb); n != read || cells != read*cols {
			t.Fatalf("before the release the cache holds %d lines of %d cells, want %d lines of %d", n, cells, read, read*cols)
		}

		gen := e.ScrollbackGeneration()
		e.ReleaseScrollbackCache()
		if n, cells := cached(sb); n != 0 || cells != 0 {
			t.Fatalf("after the release the cache holds %d lines of %d cells, want none", n, cells)
		}
		if got := e.ScrollbackGeneration(); got != gen {
			t.Fatalf("the release moved the history's generation from %d to %d, so copy mode would search it all again", gen, got)
		}
		for i := range read {
			if !reflect.DeepEqual(kept[i], copies[i]) {
				t.Fatalf("history line %d a reader held changed under the release", i)
			}
		}
		for i := range read {
			if got := e.ScrollbackLine(i); !reflect.DeepEqual(got, copies[i]) {
				t.Fatalf("history line %d reads differently after the release", i)
			}
		}
		if n, _ := cached(sb); n != read {
			t.Fatalf("reading the lines again cached %d of them, want %d", n, read)
		}
	})

	t.Run("a pane never scrolled", func(t *testing.T) {
		e, twin := filled(), filled()
		e.ReleaseScrollbackCache()
		if n, cells := cached(e.Scrollback()); n != 0 || cells != 0 {
			t.Fatalf("the cache of a pane never read holds %d lines of %d cells", n, cells)
		}
		for i := range read {
			if got, want := e.ScrollbackLine(i), twin.ScrollbackLine(i); !reflect.DeepEqual(got, want) {
				t.Fatalf("history line %d reads differently after the release", i)
			}
		}
	})

	t.Run("no scrollback", func(t *testing.T) {
		e := filled()
		e.scrs[0].DisableScrollback()
		e.ReleaseScrollbackCache()
	})

	t.Run("beside readers", func(t *testing.T) {
		const (
			readers  = 4
			rounds   = 20
			releases = 200
		)
		e := filled()
		_, copies := readLines(e)
		got := make([][]uv.Line, readers)
		var wg sync.WaitGroup
		for g := range readers {
			wg.Go(func() {
				for range rounds {
					for i := g; i < read; i += readers {
						got[g] = append(got[g], e.ScrollbackLine(i))
					}
				}
			})
		}
		for range releases {
			e.ReleaseScrollbackCache()
		}
		wg.Wait()
		for g := range readers {
			for k, line := range got[g] {
				i := g + readers*(k%(read/readers))
				if !reflect.DeepEqual(line, copies[i]) {
					t.Fatalf("reader %d read history line %d differently beside the release", g, i)
				}
			}
		}
	})
}
