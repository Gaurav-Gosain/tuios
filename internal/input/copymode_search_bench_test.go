package input

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// searchBenchWindow is a 207x55 pane whose 10000-line scrollback has wrapped
// with build-log lines, the pane the copy-mode search figures are taken on.
func searchBenchWindow(b *testing.B) *terminal.Window {
	b.Helper()
	em := vt.NewEmulator(207, 55)
	b.Cleanup(func() { _ = em.Close() })
	em.SetScrollbackMaxLines(vt.DefaultScrollbackSize)
	for i := range vt.DefaultScrollbackSize + 2000 {
		fmt.Fprintf(em, "\x1b[32m[%5d/99999]\x1b[0m CC src/module_%03d/file_%05d.c -o build/obj/%x.o -O2 -Wall\r\n",
			i, i%97, i*7919%100003, i*2654435761)
	}
	return &terminal.Window{Terminal: em, Width: 209, Height: 57, CopyMode: &terminal.CopyMode{Active: true}}
}

// BenchmarkCopyModeSearchKey is one key typed into the copy-mode search
// prompt on a full pane: the whole buffer is searched for the query so far.
// The query matches a handful of lines, so the search does not stop early at
// the match limit.
func BenchmarkCopyModeSearchKey(b *testing.B) {
	w := searchBenchWindow(b)
	cm := w.CopyMode
	b.ReportAllocs()
	for b.Loop() {
		cm.SearchCache.Valid = false
		cm.SearchQuery = "module_042/file_"
		executeSearch(cm, w)
	}
	if len(cm.SearchMatches) == 0 {
		b.Fatal("no matches")
	}
}

// BenchmarkCopyModeSearchTyping types a query one key at a time from an empty
// prompt, the way a person does: each key searches again.
func BenchmarkCopyModeSearchTyping(b *testing.B) {
	w := searchBenchWindow(b)
	cm := w.CopyMode
	const query = "module_042/file_"
	b.ReportAllocs()
	for b.Loop() {
		cm.SearchCache = terminal.SearchCache{}
		for i := 1; i <= len(query); i++ {
			cm.SearchQuery = query[:i]
			executeSearch(cm, w)
		}
	}
	if len(cm.SearchMatches) == 0 {
		b.Fatal("no matches")
	}
}
