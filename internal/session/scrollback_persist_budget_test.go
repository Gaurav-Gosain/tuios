package session

import (
	"runtime"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestHistoryCaptureBudget holds the part of a history save that holds the
// pane's emulator lock to a memory budget. The lock is held while the rows are
// copied, and every byte copied there is time the pane's output waits.
// Decoding the history rows into cells under the lock allocated 26 MB for the
// default 1000 rows of a 200-column pane. The budget is in bytes, which do
// not depend on the machine's speed.
func TestHistoryCaptureBudget(t *testing.T) {
	p := &PTY{ID: "budget", terminal: vt.NewWithScrollback(200, 50, 10000)}
	writePane(p, manyLines("budget", 3000))
	allocated := func() uint64 {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.TotalAlloc
	}
	before := allocated()
	rows, _ := p.captureHistory(DefaultHistoryLines)
	used := allocated() - before
	if rows == nil || rows.history.Len() != DefaultHistoryLines {
		t.Fatalf("the capture holds no history, want %d rows", DefaultHistoryLines)
	}
	const budget = 4 << 20
	if used > budget {
		t.Fatalf("the capture allocated %.1f MB under the pane's lock, the budget is %d MB", float64(used)/(1<<20), budget>>20)
	}
}
