package tuie2e

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// freePort reserves a port by binding and immediately releasing it. There is a
// race between release and tuios binding it, but the window is tiny and a
// collision fails loudly at startup rather than silently.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("release port: %v", err)
	}
	return port
}

// goroutineCountRe pulls the total out of the pprof goroutine profile header,
// which begins "goroutine profile: total 123".
var goroutineCountRe = regexp.MustCompile(`goroutine profile: total (\d+)`)

// pprofGoroutines asks the running tuios for its live goroutine count.
func pprofGoroutines(addr string) (int, error) {
	body, err := httpGet(fmt.Sprintf("http://%s/debug/pprof/goroutine?debug=1", addr))
	if err != nil {
		return 0, err
	}
	m := goroutineCountRe.FindStringSubmatch(body)
	if m == nil {
		return 0, fmt.Errorf("no goroutine total in profile")
	}
	return strconv.Atoi(m[1])
}

// heapInUseRe pulls the in-use bytes out of the debug=1 heap profile trailer.
var heapInUseRe = regexp.MustCompile(`# HeapInuse = (\d+)`)

// pprofHeapInUse asks the running tuios for its in-use heap in bytes.
func pprofHeapInUse(addr string) (uint64, error) {
	body, err := httpGet(fmt.Sprintf("http://%s/debug/pprof/heap?debug=1", addr))
	if err != nil {
		return 0, err
	}
	m := heapInUseRe.FindStringSubmatch(body)
	if m == nil {
		return 0, fmt.Errorf("no HeapInuse in profile")
	}
	return strconv.ParseUint(m[1], 10, 64)
}

func httpGet(url string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// TestSoakMixedActivity runs sustained mixed activity for a bounded period and
// asserts tuios neither crashes, hangs, nor grows without bound.
//
// The three failure modes are checked separately because they look different:
//
//   - A crash shows up as the process exiting, which alive() reports.
//   - A hang shows up as the UI no longer rendering new content. Each cycle
//     ends by demanding a marker the shell computes, so a frozen UI fails the
//     cycle it froze on rather than passing quietly for the full duration. This
//     is the same shape as the deadlock regression test, run under load.
//   - Runaway growth shows up in the goroutine count and in-use heap, read from
//     the process's own pprof endpoint. Goroutines are the sharper signal: a
//     leak per window, per focus change, or per resize accumulates
//     monotonically and is not hidden by GC timing.
//
// The activity deliberately mixes the operations whose interactions caused this
// session's bugs: output bursts, focus changes, tiling, zoom, resize, and
// window create/close.
func TestSoakMixedActivity(t *testing.T) {
	if testing.Short() {
		t.Skip("soak test skipped in -short mode")
	}

	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	term, _ := start(t, startOpts{
		args: []string{"--shared-borders", "--pprof", addr},
	})
	waitBoot(t, term)

	// Two windows with identifiable content, tiled.
	for i := 1; i <= 2; i++ {
		newWindow(t, term)
		waitWindowCount(t, term, i, fmt.Sprintf("soak setup window %d", i))
	}
	enableTiling(t, term)

	// Let startup settle before taking the baseline, so one-time initialisation
	// is not counted as growth.
	time.Sleep(3 * time.Second)
	baseGoroutines, err := pprofGoroutines(addr)
	if err != nil {
		t.Fatalf("baseline goroutine count: %v", err)
	}
	baseHeap, err := pprofHeapInUse(addr)
	if err != nil {
		t.Fatalf("baseline heap: %v", err)
	}
	t.Logf("baseline: %d goroutines, %.1f MiB heap in use",
		baseGoroutines, float64(baseHeap)/(1<<20))

	const cycles = 8
	sizes := [][2]int{{120, 40}, {100, 30}, {140, 46}, {90, 28}}

	for cycle := 1; cycle <= cycles; cycle++ {
		// Output burst in the focused pane.
		enterTerminalMode(t, term)
		marker := fmt.Sprintf("SOAK-%d-DONE", cycle)
		cmd := fmt.Sprintf("for i in $(seq 1 120); do echo soak-%d-$i; done; echo SOAK-$((%d))-DONE",
			cycle, cycle)
		if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
			t.Fatalf("cycle %d: send burst: %v", cycle, err)
		}
		// A frozen UI never shows this, so the hang check is per cycle.
		if err := term.WaitForText(marker, 30*time.Second); err != nil {
			t.Fatalf("UI stopped rendering during soak cycle %d/%d: %q never appeared: %v\n%s",
				cycle, cycles, marker, err, term.Snapshot())
		}
		leaveTerminalMode(t, term)

		// Focus churn.
		for range 8 {
			if err := term.SendKeys(tuitest.Tab); err != nil {
				t.Fatalf("cycle %d: tab: %v", cycle, err)
			}
		}
		// Zoom in and out.
		for range 2 {
			if err := term.SendKeys(tuitest.Ctrl('b'), "z"); err != nil {
				t.Fatalf("cycle %d: zoom: %v", cycle, err)
			}
			time.Sleep(120 * time.Millisecond)
		}
		// Resize.
		sz := sizes[cycle%len(sizes)]
		if err := term.Resize(sz[0], sz[1]); err != nil {
			t.Fatalf("cycle %d: resize to %dx%d failed (tuios likely died): %v\n%s",
				cycle, sz[0], sz[1], err, term.Snapshot())
		}
		// Create and close a window, so window lifecycle churns too.
		newWindow(t, term)
		waitWindowCount(t, term, 3, fmt.Sprintf("soak cycle %d: transient window", cycle))
		if err := term.SendKeys("x"); err != nil {
			t.Fatalf("cycle %d: close transient window: %v", cycle, err)
		}
		waitWindowCount(t, term, 2, fmt.Sprintf("soak cycle %d: after closing transient window", cycle))

		alive(t, term, fmt.Sprintf("during soak cycle %d", cycle))
	}

	// Back to the starting size and let things quiesce so the comparison is
	// like for like, and give the GC a chance to return what is genuinely free.
	if err := term.Resize(120, 40); err != nil {
		t.Fatalf("final resize: %v", err)
	}
	time.Sleep(3 * time.Second)

	endGoroutines, err := pprofGoroutines(addr)
	if err != nil {
		t.Fatalf("final goroutine count: %v", err)
	}
	endHeap, err := pprofHeapInUse(addr)
	if err != nil {
		t.Fatalf("final heap: %v", err)
	}
	t.Logf("after %d cycles: %d goroutines (from %d), %.1f MiB heap (from %.1f MiB)",
		cycles, endGoroutines, baseGoroutines,
		float64(endHeap)/(1<<20), float64(baseHeap)/(1<<20))

	// The window count is back where it started, so the goroutine count should
	// be too. The allowance covers per-window and per-client goroutines that
	// legitimately come and go, without tolerating a per-cycle leak: with 8
	// cycles, a leak of even two goroutines per cycle exceeds it.
	const goroutineAllowance = 12
	if endGoroutines > baseGoroutines+goroutineAllowance {
		profile, _ := httpGet(fmt.Sprintf("http://%s/debug/pprof/goroutine?debug=1", addr))
		t.Fatalf("goroutine count grew from %d to %d over %d cycles (allowance %d): "+
			"something is leaking a goroutine per cycle\n%s",
			baseGoroutines, endGoroutines, cycles, goroutineAllowance, topGoroutineStacks(profile))
	}

	// Heap is noisier than goroutine count because scrollback genuinely grows
	// with the output produced, so the bound is a ratio and generous. It is
	// there to catch an unbounded leak, not to police allocation.
	if endHeap > baseHeap*4 && endHeap-baseHeap > 64<<20 {
		t.Fatalf("in-use heap grew from %.1f MiB to %.1f MiB over %d cycles, "+
			"more than 4x and more than 64 MiB: this looks unbounded",
			float64(baseHeap)/(1<<20), float64(endHeap)/(1<<20), cycles)
	}

	// Finally, the UI must still work after the whole soak.
	enterTerminalMode(t, term)
	runInShell(t, term, "echo SOAK-FINAL-$((11*11))", "SOAK-FINAL-121", 30*time.Second)
	alive(t, term, "at the end of the soak")
}

// topGoroutineStacks trims a pprof goroutine profile to its first few stacks,
// which is enough to name the leak in a failure message without dumping
// thousands of lines into CI output.
func topGoroutineStacks(profile string) string {
	lines := strings.Split(profile, "\n")
	if len(lines) > 60 {
		lines = lines[:60]
	}
	return strings.Join(lines, "\n")
}
