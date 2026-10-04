package input

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestCopyModeSearchKeyBudget holds one key typed into the copy-mode search
// prompt to a memory budget on a full 207-column pane. Each key searches the
// whole buffer, and decoding every history line into cells to do it
// allocated 270 MB a key. The budget is in bytes, which do not depend on the
// machine's speed.
func TestCopyModeSearchKeyBudget(t *testing.T) {
	em := vt.NewEmulator(207, 55)
	t.Cleanup(func() { _ = em.Close() })
	em.SetScrollbackMaxLines(vt.DefaultScrollbackSize)
	for i := range vt.DefaultScrollbackSize + 100 {
		fmt.Fprintf(em, "\x1b[32m[%5d]\x1b[0m CC src/module_%03d/file.c -o build/obj/%x.o -O2 -Wall\r\n", i, i%97, i*2654435761)
	}
	w := &terminal.Window{Terminal: em, Width: 209, Height: 57, CopyMode: &terminal.CopyMode{Active: true}}
	cm := w.CopyMode
	cm.SearchQuery = "module_042/"
	allocated := func() uint64 {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.TotalAlloc
	}
	before := allocated()
	executeSearch(cm, w)
	used := allocated() - before
	if len(cm.SearchMatches) < 100 {
		t.Fatalf("the search found %d matches, want one per 97 lines", len(cm.SearchMatches))
	}
	const budget = 4 << 20
	if used > budget {
		t.Fatalf("one search key allocated %.1f MB, the budget is %d MB", float64(used)/(1<<20), budget>>20)
	}
}
