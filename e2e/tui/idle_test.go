package tuie2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// byteCounter tallies bytes written to it. tuitest mirrors PTY traffic from its
// own reader goroutine, so the tally is atomic.
type byteCounter struct{ n atomic.Int64 }

func (b *byteCounter) Write(p []byte) (int, error) {
	b.n.Add(int64(len(p)))
	return len(p), nil
}

// idleWireBudget bounds the bytes tuios may write during a 10s idle window. A
// held frame-skip writes nothing; the budget tolerates a stray settling frame
// or two but is far below what a leaking 10Hz render of changing content costs.
const idleWireBudget = 2048

// TestIdleCostStaysLow is the idle-cost regression guard for the whole program:
// one client, three idle shells, clock off. Over a 10s idle window the frame
// skip must hold, so tuios writes ~nothing to the wire. A future milestone that
// reintroduces a timer-driven render trips this. Tick-work at idle is guarded
// precisely in-process by BenchmarkIdleTick / TestIdleTickSkipsScans; this test
// guards the render count on the real binary. See docs/perf.md.
func TestIdleCostStaysLow(t *testing.T) {
	var wire byteCounter
	statsPath := filepath.Join(t.TempDir(), "tickstats")
	term, _ := start(t, startOpts{
		out: &wire,
		env: []string{"TUIOS_STATS_FILE=" + statsPath},
	})
	waitBoot(t, term)

	newWindow(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 3, "opening three idle shells")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled before idle: %v\n%s", err, term.Snapshot())
	}

	before := wire.n.Load()
	time.Sleep(10 * time.Second)
	idleBytes := wire.n.Load() - before
	t.Logf("idle wire bytes over 10s: %d (budget %d)", idleBytes, idleWireBudget)
	if idleBytes > idleWireBudget {
		t.Fatalf("idle wrote %d bytes to the wire (budget %d); a timer-driven render is leaking\n%s",
			idleBytes, idleWireBudget, term.Snapshot())
	}

	// Clean quit so the tick counters land in the stats file. Standalone with
	// only idle shells quits instantly on leader-q, no confirmation.
	if err := term.SendKeys(tuitest.Ctrl('b'), "q"); err != nil {
		t.Fatalf("send leader q: %v", err)
	}
	waitExit(t, term, "idle test quit")

	ticks, work, render := readTickStats(t, statsPath)
	t.Logf("tick stats over the run: ticks=%d work=%d render=%d", ticks, work, render)
	if ticks == 0 {
		t.Fatalf("stats file recorded zero ticks; the counter pipeline is broken")
	}
	// The 10s idle window is ~100 ticks that the diet must skip, so scan work
	// stays far below the tick count. Without the diet work == ticks.
	if work >= ticks {
		t.Fatalf("tick work %d did not fall below tick count %d; the idle diet is not skipping scans", work, ticks)
	}
}

// readTickStats parses the "ticks=N work=N render=N" line DumpTickStats writes.
func readTickStats(t *testing.T, path string) (ticks, work, render uint64) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tick stats %s: %v", path, err)
	}
	for _, field := range strings.Fields(strings.TrimSpace(string(data))) {
		k, v, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		n, _ := strconv.ParseUint(v, 10, 64)
		switch k {
		case "ticks":
			ticks = n
		case "work":
			work = n
		case "render":
			render = n
		}
	}
	return ticks, work, render
}
