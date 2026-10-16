package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
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

// idleWindow is how long TestIdleCostStaysLow leaves the client alone.
const idleWindow = 10 * time.Second

// idleBudget is what one idle window of TestIdleCostStaysLow may cost. Each
// count is the difference between the client's counters at the two ends of the
// window.
type idleBudget struct {
	// wire is the most bytes written to the terminal, and minWire the
	// fewest: a clock that ticks must reach the screen.
	wire, minWire int64
	// ticks is the maintenance ticks handled, and work those that ran the
	// full scans.
	ticks, work uint64
	// tickerWakes is how often a frame woke Bubble Tea's frame ticker to the
	// frame rate.
	tickerWakes uint64
	// ctxsw is the client's voluntary context switches, all threads, on
	// Linux only.
	ctxsw int64
}

// TestIdleCostStaysLow is the idle-cost regression guard for the whole program:
// one client, three idle shells. Over a 10s idle window the client must write
// almost nothing, and it must not wake for nothing: the maintenance ticks, the
// frame ticker wakes, and on Linux the context switches each have a budget.
//
// With the clock on, the dock draws one frame a second. Those frames go out
// through kickFlush and must not wake the frame ticker: before they did, a
// clock with seconds cost about 35 frame ticks a second for one frame.
//
// Negative controls are in NEGATIVE_CONTROLS.md. In-process, tick work at idle
// is guarded by BenchmarkIdleTick and TestIdleTickSkipsScans. See docs/perf.md.
func TestIdleCostStaysLow(t *testing.T) {
	t.Run("clock off", func(t *testing.T) {
		runIdleWindow(t, "", idleBudget{
			wire: idleWireBudget,
			// IdleFPS is 10 a second, plus slack for a slow machine.
			ticks: 110, work: 1,
			tickerWakes: 1,
			ctxsw:       2500,
		})
	})
	t.Run("clock on", func(t *testing.T) {
		runIdleWindow(t, "[appearance]\nshow_clock = true\n", idleBudget{
			// Ten clock frames, and they must go out.
			wire: 4 * idleWireBudget, minWire: 100,
			ticks: 110, work: 1,
			// Was 10, one per clock frame.
			tickerWakes: 1,
			ctxsw:       2500,
		})
	})
}

// runIdleWindow starts a client with config (none when empty), opens three
// idle shells, and holds one idle window to budget.
func runIdleWindow(t *testing.T, config string, budget idleBudget) {
	base := t.TempDir()
	if config != "" {
		writeConfig(t, base, config)
	}
	var wire byteCounter
	statsPath := filepath.Join(base, "tickstats")
	term := startIn(t, base, startOpts{
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
	// Past idleTickerAfter, so the frame ticker has gone idle.
	time.Sleep(time.Second)

	pid := term.Pid()
	start := sampleIdle(t, pid, statsPath)
	before := wire.n.Load()
	time.Sleep(idleWindow)
	idleBytes := wire.n.Load() - before
	end := sampleIdle(t, pid, statsPath)

	got := idleBudget{
		wire:        idleBytes,
		ticks:       end.stats["ticks"] - start.stats["ticks"],
		work:        end.stats["work"] - start.stats["work"],
		tickerWakes: end.stats["tickerWakes"] - start.stats["tickerWakes"],
		ctxsw:       -1,
	}
	if start.ctxsw >= 0 && end.ctxsw >= 0 {
		got.ctxsw = end.ctxsw - start.ctxsw
	}
	t.Logf("idle window %v: wire=%dB ticks=%d work=%d tickerWakes=%d ctxsw=%d",
		idleWindow, got.wire, got.ticks, got.work, got.tickerWakes, got.ctxsw)

	var over []string
	if got.wire > budget.wire {
		over = append(over, fmt.Sprintf("wrote %d bytes (budget %d): a timer-driven render is leaking", got.wire, budget.wire))
	}
	if got.wire < budget.minWire {
		over = append(over, fmt.Sprintf("wrote %d bytes, under the %d the clock's frames take: they did not go out", got.wire, budget.minWire))
	}
	if got.ticks > budget.ticks {
		over = append(over, fmt.Sprintf("handled %d maintenance ticks (budget %d)", got.ticks, budget.ticks))
	}
	if got.work > budget.work {
		over = append(over, fmt.Sprintf("%d ticks ran the full scans (budget %d): the idle gate is not holding", got.work, budget.work))
	}
	if got.tickerWakes > budget.tickerWakes {
		over = append(over, fmt.Sprintf("woke the frame ticker %d times (budget %d)", got.tickerWakes, budget.tickerWakes))
	}
	if got.ctxsw > budget.ctxsw {
		over = append(over, fmt.Sprintf("made %d context switches (budget %d)", got.ctxsw, budget.ctxsw))
	}
	if start.stats["ticks"] == 0 && end.stats["ticks"] == 0 && start.stats["render"] == 0 {
		over = append(over, "the stats file holds only zeros; the counter pipeline is broken")
	}
	if len(over) > 0 {
		t.Fatalf("idle window over budget:\n  %s\n%s", strings.Join(over, "\n  "), term.Snapshot())
	}

	// Standalone with only idle shells quits at once on leader-q.
	if err := term.SendKeys(tuitest.Ctrl('b'), "q"); err != nil {
		t.Fatalf("send leader q: %v", err)
	}
	waitExit(t, term, "idle test quit")
}

// idleSample is a client's counters at one moment.
type idleSample struct {
	// stats is the line DumpTickStats writes, by name.
	stats map[string]uint64
	// ctxsw is the context switches of all its threads, -1 without /proc.
	ctxsw int64
}

// sampleIdle asks the client for its tick counters with SIGUSR1, waits for
// the file, and reads the client's context switches next to it.
func sampleIdle(t *testing.T, pid int, statsPath string) idleSample {
	t.Helper()
	_ = os.Remove(statsPath)
	if err := syscall.Kill(pid, syscall.SIGUSR1); err != nil {
		t.Fatalf("signal the client for its counters: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var data []byte
	for {
		var err error
		data, err = os.ReadFile(statsPath)
		if err == nil && strings.HasSuffix(string(data), "\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the client wrote no counters within 5s of SIGUSR1 (%v)", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	s := idleSample{stats: parseTickStats(string(data)), ctxsw: procCtxSwitches(pid)}
	return s
}

// parseTickStats reads a "name=N name=N" line.
func parseTickStats(line string) map[string]uint64 {
	out := map[string]uint64{}
	for field := range strings.FieldsSeq(line) {
		k, v, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		n, _ := strconv.ParseUint(v, 10, 64)
		out[k] = n
	}
	return out
}

// procCtxSwitches sums the voluntary context switches of every thread of pid,
// -1 where there is no /proc. A voluntary switch is a thread that went to
// sleep, which at idle is a wakeup that came before it. The involuntary ones
// count how busy the machine is, not the client.
func procCtxSwitches(pid int) int64 {
	dir := filepath.Join("/proc", strconv.Itoa(pid), "task")
	tasks, err := os.ReadDir(dir)
	if err != nil {
		return -1
	}
	var n int64
	for _, task := range tasks {
		data, err := os.ReadFile(filepath.Join(dir, task.Name(), "status"))
		if err != nil {
			continue // the thread ended between the two reads
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			if rest, ok := strings.CutPrefix(line, "voluntary_ctxt_switches:"); ok {
				v, _ := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
				n += v
			}
		}
	}
	return n
}

// readTickStats parses the "ticks=N work=N render=N" line DumpTickStats writes.
func readTickStats(t *testing.T, path string) (ticks, work, render uint64) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tick stats %s: %v", path, err)
	}
	st := parseTickStats(string(data))
	return st["ticks"], st["work"], st["render"]
}
